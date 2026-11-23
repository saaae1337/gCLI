package tools

// Взаимодействие с компьютером: буфер обмена, открытие ссылок и файлов,
// уведомления, снимок экрана.
//
// Все четыре инструмента — мост в мир вне рабочего каталога. В отличие от
// bash, где модель сама пишет команду, здесь команда собрана программно:
// меньше свободы — меньше мест для неожиданного поведения. Поэтому у
// каждого инструмента честный набор ограничений:
//
//   - clipboard читает буфер через маскирование секретов: в буфере часто
//     лежат пароли и токены, и они не должны попадать в контекст модели;
//   - open открывает только http/https и файлы внутри песочницы;
//   - screen — снимок РАБОЧЕГО СТОЛА пользователя, а не страницы: требует
//     подтверждения и не кешируется (кеш категории read вернул бы устаревший
//     кадр — экран меняется вне контроля отпечатков файлов);
//   - notify ничего не возвращает в модель: результат проверки — молчание,
//     значит, уведомление отправлено.
//
// Платформенная часть собрана в этом же файле: все ветки простые вызовы
// системных утилит, и разводить их по файлам ради двух веток не стоит.

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gcli/core"
)

// Схемы инструментов компьютера.
const (
	schemaClipboard = `{"type":"object","properties":{"action":{"type":"string","enum":["read","write","clear"],"description":"read - прочитать буфер обмена; write - записать текст; clear - очистить (по умолчанию read)"},"text":{"type":"string","description":"Текст для action=write"}}}`
	schemaOpen      = `{"type":"object","properties":{"target":{"type":"string","description":"URL (http/https) или путь к файлу внутри проекта - открыть системным обработчиком: браузером, просмотрщиком, редактором"},"wait":{"type":"boolean","description":"Ждать закрытия открытой программы (по умолчанию нет)"}},"required":["target"]}`
	schemaNotify    = `{"type":"object","properties":{"title":{"type":"string","description":"Заголовок уведомления"},"message":{"type":"string","description":"Текст уведомления - что случилось и что с этим делать"}},"required":["title","message"]}`
	schemaScreen    = `{"type":"object","properties":{"out":{"type":"string","description":"Куда сохранить PNG (по умолчанию .gcli/screenshots/)"},"monitor":{"type":"string","description":"all - все мониторы (по умолчанию), main - основной"}},"description":"Снимок рабочего стола пользователя. Требует подтверждения: это снимок того, что человек видит на экране."}`
)

// registerComputer — инструменты взаимодействия с компьютером.
func (r *Registry) registerComputer() {
	r.registerBound("clipboard", "Буфер обмена компьютера: read - прочитать (секреты маскируются), write - записать текст, clear - очистить. Полезно, когда пользователь просит что-то скопировать или вставить, и для переноса текста между программами.",
		schemaClipboard, "write", false, func(r *Registry) Handler { return r.hClipboard })
	r.registerBound("open", "Открыть URL или файл системным обработчиком: ссылка - в браузере, картинка - в просмотрщике, каталог - в файловом менеджере. Сам не читает содержимое: для текста в контексте есть read_file и web_fetch.",
		schemaOpen, "exec", true, func(r *Registry) Handler { return r.hOpen })
	r.registerBound("notify", "Уведомление на рабочем столе пользователя: долгая команда завершилась, ход закончился, нужно решение. Ничего не возвращает: отправка считается удачной, если не было ошибки.",
		schemaNotify, "exec", false, func(r *Registry) Handler { return r.hNotify })
	r.registerBound("screen", "Снимок рабочего стола компьютера (всех мониторов или основного) - картинкой в контекст. Для снимка страницы есть screenshot. Относись к результату как к личным данным пользователя: без необходимости не вызывай.",
		schemaScreen, "exec", true, func(r *Registry) Handler { return r.hScreen })
}

// ---------- Общие помощники ----------

// maxClipboardText — потолок текста из буфера обмена: больше модели не нужно,
// а бесконечная вставка была бы бесплатной дырой в контекст.
const maxClipboardText = 32 * 1024

// runCapture — запустить команду и забрать stdout; stderr — в ошибку.
func runCapture(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s: %s", name, core.OneLine(msg))
	}
	return out.String(), nil
}

// powershellArgs — вызов PowerShell без кавычек и с уважением к юникоду.
//
// Текст команды передаётся как -EncodedCommand (base64 от UTF-16LE):
// обычный -Command ломался бы на кавычках внутри уведомления или пути,
// а на Windows-консоли без UTF-8 — ещё и на кириллице.
func powershellArgs(script string) (string, []string) {
	enc := base64.StdEncoding.EncodeToString([]byte(script))
	return "powershell", []string{"-NoProfile", "-NonInteractive", "-EncodedCommand", enc}
}

// psQuote — экранировать строку для одинарных кавычек PowerShell.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// ---------- clipboard ----------

// hClipboard — буфер обмена: read/write/clear.
func (r *Registry) hClipboard(ctx context.Context, m map[string]any) (Result, error) {
	action := strings.ToLower(ArgStr(m, "action"))
	if action == "" {
		action = "read"
	}
	switch action {
	case "read":
		text, err := clipboardRead(ctx)
		if err != nil {
			return Result{}, err
		}
		if len([]rune(text)) > maxClipboardText {
			text = core.TruncateUTF8(text, maxClipboardText/2, maxClipboardText/4)
		}
		if strings.TrimSpace(text) == "" {
			return Result{Text: "Буфер обмена пуст.", Summary: "буфер пуст"}, nil
		}
		// В буфере часто лежат пароли и токены: маскируем по форме —
		// так же, как вывод bash и содержимое файлов.
		text = RedactSecrets(text)
		return Result{
			Text:    "Буфер обмена:\n" + text,
			Summary: "буфер: " + core.Truncate(core.OneLine(text), 50),
		}, nil
	case "write":
		text := ArgStr(m, "text")
		if text == "" {
			return Result{}, fmt.Errorf("укажи text для action=write")
		}
		if err := clipboardWrite(ctx, text); err != nil {
			return Result{}, err
		}
		return Result{
			Text:    fmt.Sprintf("В буфер обмена записано %d символов.", len([]rune(text))),
			Summary: "буфер записан",
		}, nil
	case "clear":
		if err := clipboardWrite(ctx, ""); err != nil {
			return Result{}, err
		}
		return Result{Text: "Буфер обмена очищен.", Summary: "буфер очищен"}, nil
	default:
		return Result{}, fmt.Errorf("action: read, write или clear")
	}
}

// clipboardRead — прочитать буфер обмена системной утилитой.
func clipboardRead(ctx context.Context) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return runCapture(ctx, 5*time.Second, "pbpaste")
	case "windows":
		name, args := powershellArgs("Get-Clipboard -Raw")
		return runCapture(ctx, 10*time.Second, name, args...)
	}
	// Linux и прочие: X11 → xclip/xsel, Wayland → wl-paste.
	wayland := os.Getenv("WAYLAND_DISPLAY") != ""
	x11 := os.Getenv("DISPLAY") != ""
	if wayland {
		if _, err := exec.LookPath("wl-paste"); err == nil {
			if t, err := runCapture(ctx, 5*time.Second, "wl-paste", "--no-newline"); err == nil {
				return t, nil
			}
		}
	}
	if x11 {
		if _, err := exec.LookPath("xclip"); err == nil {
			if t, err := runCapture(ctx, 5*time.Second, "xclip", "-selection", "clipboard", "-o"); err == nil {
				return t, nil
			}
		}
		if _, err := exec.LookPath("xsel"); err == nil {
			if t, err := runCapture(ctx, 5*time.Second, "xsel", "-b", "-o"); err == nil {
				return t, nil
			}
		}
	}
	return "", fmt.Errorf("буфер обмена недоступен: не найден xclip, xsel или wl-paste (установи xclip или wl-clipboard)")
}

// clipboardWrite — записать текст в буфер обмена.
func clipboardWrite(ctx context.Context, text string) error {
	switch runtime.GOOS {
	case "darwin":
		return runStdin(ctx, "pbcopy", text)
	case "windows":
		name, args := powershellArgs("Set-Clipboard -Value " + psQuote(text))
		_, err := runCapture(ctx, 10*time.Second, name, args...)
		return err
	}
	wayland := os.Getenv("WAYLAND_DISPLAY") != ""
	x11 := os.Getenv("DISPLAY") != ""
	if wayland {
		if _, err := exec.LookPath("wl-copy"); err == nil {
			return runStdin(ctx, "wl-copy", text)
		}
	}
	if x11 {
		if _, err := exec.LookPath("xclip"); err == nil {
			return runStdin(ctx, "xclip", text, "-selection", "clipboard", "-i")
		}
	}
	return fmt.Errorf("буфер обмена недоступен: не найден xclip или wl-copy (установи xclip или wl-clipboard)")
}

// runStdin — передать текст утилите через stdin (pbcopy, wl-copy, xclip -i).
// Имя команды — первым аргументом, как в runCapture: раньше текст стоял
// раньше имени, вызов runStdin(ctx, "pbcopy", text) пытался исполнить
// текст буфера как программу (exec: «x»: executable file not found),
// а до pbcopy строка не доходила вовсе.
func runStdin(ctx context.Context, name, text string, args ...string) error {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// ---------- open ----------

// hOpen — открыть URL или файл системным обработчиком.
func (r *Registry) hOpen(ctx context.Context, m map[string]any) (Result, error) {
	target := strings.TrimSpace(ArgStr(m, "target"))
	if target == "" {
		return Result{}, fmt.Errorf("укажи target - URL или путь")
	}
	wait := ArgBool(m, "wait")
	if strings.Contains(target, "://") {
		if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
			return Result{}, fmt.Errorf("открываются только http:// и https:// - %q не поддерживается", core.OneLine(target))
		}
	} else {
		// Не URL — путь. Песочница режет его как read_file: иначе open
		// стал бы бесплатной записью «куда угодно» в просмотрщике.
		p, err := r.pathArg(target)
		if err != nil {
			return Result{}, err
		}
		if _, err := os.Stat(p); err != nil {
			return Result{}, fmt.Errorf("файл не найден: %s", p)
		}
		target = p
	}
	if err := openSystem(ctx, target, wait); err != nil {
		return Result{}, err
	}
	return Result{
		Text:    fmt.Sprintf("Открыто системным обработчиком: %s", target),
		Summary: "открыто: " + core.Truncate(core.OneLine(filepath.Base(target)), 50),
	}, nil
}

// openSystem — платформенный вызов системного обработчика.
func openSystem(ctx context.Context, target string, wait bool) error {
	switch runtime.GOOS {
	case "darwin":
		if wait {
			return exec.CommandContext(ctx, "open", "-W", target).Run()
		}
		return exec.CommandContext(ctx, "open", target).Start()
	case "windows":
		// rundll32 вместо «cmd /c start»: без промежуточной консоли и
		// без разбора амперсандов в URL командной строкой.
		return exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", target).Start()
	}
	bin, err := exec.LookPath("xdg-open")
	if err != nil {
		return fmt.Errorf("xdg-open не найден - на этой системе открывать файлы нечем")
	}
	return exec.CommandContext(ctx, bin, target).Start()
}

// ---------- notify ----------

// hNotify — уведомление на рабочем столе.
func (r *Registry) hNotify(ctx context.Context, m map[string]any) (Result, error) {
	title := strings.TrimSpace(ArgStr(m, "title"))
	message := strings.TrimSpace(ArgStr(m, "message"))
	if title == "" {
		return Result{}, fmt.Errorf("укажи title")
	}
	if len([]rune(title)) > 80 {
		title = string([]rune(title)[:80]) + "..."
	}
	if len([]rune(message)) > 300 {
		message = string([]rune(message)[:300]) + "..."
	}
	if err := notifySystem(ctx, title, message); err != nil {
		return Result{}, err
	}
	return Result{
		Text:    fmt.Sprintf("Уведомление отправлено: \"%s\".", title),
		Summary: "уведомление: " + title,
	}, nil
}

// notifySystem — платформенное уведомление.
func notifySystem(ctx context.Context, title, message string) error {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`display notification %s with title %s`,
			quoteApple(message), quoteApple(title))
		return exec.CommandContext(ctx, "osascript", "-e", script).Run()
	case "windows":
		script := `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$n = New-Object System.Windows.Forms.NotifyIcon
$n.Icon = [System.Drawing.SystemIcons]::Information
$n.Visible = $true
$n.ShowBalloonTip(8000, ` + psQuote(title) + `, ` + psQuote(message) + `, [System.Windows.Forms.ToolTipIcon]::Info)
Start-Sleep -Seconds 11
$n.Dispose()
`
		name, args := powershellArgs(script)
		_, err := runCapture(ctx, 20*time.Second, name, args...)
		return err
	}
	if _, err := exec.LookPath("notify-send"); err != nil {
		return fmt.Errorf("notify-send не найден (пакет libnotify) - уведомления недоступны")
	}
	// Аргументы передаются отдельными параметрами: кавычки внутри текста
	// безопасны без экранирования.
	return exec.CommandContext(ctx, "notify-send", "-a", "gcli", title, message).Run()
}

// quoteApple — экранировать строку для AppleScript.
func quoteApple(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// ---------- screen ----------

// hScreen — снимок рабочего стола пользователя.
func (r *Registry) hScreen(ctx context.Context, m map[string]any) (Result, error) {
	out := strings.TrimSpace(ArgStr(m, "out"))
	if out == "" {
		out = filepath.Join(".gcli", "screenshots", "screen-"+core.RandID(5)+".png")
	}
	outPath, err := r.pathArg(out)
	if err != nil {
		return Result{}, err
	}
	if filepath.Ext(outPath) == "" {
		outPath += ".png"
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return Result{}, fmt.Errorf("не удалось создать каталог для снимка: %v", err)
	}
	if err := screenCapture(ctx, outPath); err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		return Result{}, fmt.Errorf("снимок сделан, но не прочитан: %v", err)
	}
	if len(data) == 0 {
		return Result{}, fmt.Errorf("снимок пустой - утилита захвата вернулась без кадра")
	}
	return Result{
		Text: fmt.Sprintf(
			"Снимок рабочего стола приложен к контексту: %s (%s). Это то, что видит пользователь на экране.",
			filepath.Base(outPath), core.HumanSize(len(data))),
		Summary: "снимок экрана: " + filepath.Base(outPath),
		Images: []core.Image{{
			MIME: "image/png",
			Data: base64.StdEncoding.EncodeToString(data),
			Path: outPath,
		}},
	}, nil
}

// screenCapture — платформенный снимок рабочего стола в PNG-файл.
func screenCapture(ctx context.Context, path string) error {
	switch runtime.GOOS {
	case "darwin":
		// -x — без звука затвора: уведомлять пользователя о снимке
		// должно подтверждение инструмента, а не внезапный щелчок.
		_, err := runCapture(ctx, 20*time.Second, "screencapture", "-x", path)
		return err
	case "windows":
		script := `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$b = [System.Windows.Forms.SystemInformation]::VirtualScreen
$bmp = New-Object System.Drawing.Bitmap $b.Width, $b.Height
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($b.Left, $b.Top, 0, 0, $bmp.Size)
$bmp.Save(` + psQuote(path) + `, [System.Drawing.Imaging.ImageFormat]::Png)
$g.Dispose()
$bmp.Dispose()
`
		name, args := powershellArgs(script)
		_, err := runCapture(ctx, 30*time.Second, name, args...)
		return err
	}
	// Linux: пробуем по убыванию вероятности присутствия. WAYLAND_DISPLAY
	// не проверяем строго: gnome-screenshot и scrot работают и там, и там.
	for _, c := range []struct {
		bin  string
		args []string
	}{
		{"gnome-screenshot", []string{"-f", path}},
		{"scrot", []string{"-o", path}},
		{"maim", []string{path}},
		{"import", []string{"-window", "root", path}},
	} {
		if _, err := exec.LookPath(c.bin); err != nil {
			continue
		}
		if _, err := runCapture(ctx, 20*time.Second, c.bin, c.args...); err == nil {
			return nil
		}
	}
	return fmt.Errorf("снимок экрана недоступен: не найден gnome-screenshot, scrot, maim или import (ImageMagick)")
}
