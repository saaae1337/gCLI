package tools

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gcli/core"
)

// ---------- Зрение агента: скриншоты и просмотр изображений ----------
//
// У агента нет глаз: сверстанный сайт он видит только как код. Инструменты
// ниже дают ему зрение:
//
//      screenshot — снимок страницы (URL или локальный файл) через headless-
//                   браузер (Edge/Chrome/Chromium): PNG прикладывается к
//                   контексту, модель видит картинку на следующем шаге;
//      read_image — чтение локального изображения (PNG/JPG/WebP/GIF) в контекст.
//
// Картинки уходят в API как обычные image-блоки (OpenAI: image_url с
// data-URL, Anthropic: блок image с base64-источником).

// maxImageBytes — потолок размера исходного файла изображения (~5 МБ base64
// у Anthropic, у OpenAI похоже; берём с запасом меньше лимита API).
const maxImageBytes = 4 * 1024 * 1024

// fullPageHeight — высота окна для снимка всей страницы.
//
// Страница длиннее 8000 px — редкость; если она длиннее, снизу останется
// хвост, но верх (шапка, первый экран) виден целиком.
const fullPageHeight = 8000

// mimeByExt — MIME по расширению файла.
var mimeByExt = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".gif":  "image/gif",
}

const schemaScreenshot = `{"type":"object","properties":{` +
	`"target":{"type":"string","description":"URL (https://...) или путь к HTML-файлу страницы"},` +
	`"out":{"type":"string","description":"Куда сохранить PNG (необязательно; по умолчанию .gcli/screenshots)"},` +
	`"width":{"type":"integer","description":"Ширина вьюпорта в CSS-пикселях (по умолчанию 1440). Для мобильной проверки — 390."},` +
	`"height":{"type":"integer","description":"Высота вьюпорта в CSS-пикселях (по умолчанию 900)"},` +
	`"full_page":{"type":"boolean","description":"Снимок всей страницы целиком: ширина та же, высота окна увеличивается. Используй, чтобы увидеть низ страницы, не прокручивая её по кускам."}` +
	`},"required":["target"]}`

const schemaReadImage = `{"type":"object","properties":{` +
	`"path":{"type":"string","description":"Путь к изображению (png, jpg, webp, gif)"}` +
	`},"required":["path"]}`

// RegisterVision — инструменты зрения.
func (r *Registry) RegisterVision() {
	r.registerBound("screenshot", "Сделать скриншот страницы и ПОСМОТРЕТЬ на него глазами: снимок прикладывается к твоему контексту как изображение. "+
		"Используй после вёрстки/изменения UI (target — URL или HTML-файл), чтобы проверить, как страница выглядит на самом деле. "+
		"width — ширина вьюпорта в CSS-пикселях (390 — мобильный, 1440 — десктоп), full_page — снимок всей страницы целиком.",
		schemaScreenshot, "read", false, func(r *Registry) Handler { return r.hScreenshot })
	r.registerBound("read_image", "Посмотреть на изображение с диска (png, jpg, webp, gif): файл прикладывается к контексту как изображение. "+
		"Используй для макетов, скриншотов пользователя, схем.",
		schemaReadImage, "read", false, func(r *Registry) Handler { return r.hReadImage })
}

// browserNeedsNoSandbox — запускать ли Chromium с --no-sandbox.
//
// Песочница рендера требует setuid-помощника или user-namespace; под root
// и в контейнерах без них Chromium падает на старте (typical CI: signal:
// aborted, core dumped). Явное GCLI_BROWSER_NO_SANDBOX включает флаг и на
// обычной машине — например, для окружений с кривыми правами.
func browserNeedsNoSandbox() bool {
	if os.Getenv("GCLI_BROWSER_NO_SANDBOX") != "" {
		return true
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return false
	}
	return os.Geteuid() == 0
}

// findBrowser — найти headless-браузер для скриншотов.
func findBrowser() (string, []string) {
	if p := strings.TrimSpace(os.Getenv("GCLI_BROWSER")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	switch runtime.GOOS {
	case "windows":
		candidates := []string{
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files\Chromium\Application\chromium.exe`,
			filepath.Join(core.Home(), `AppData\Local\Google\Chrome\Application\chrome.exe`),
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
		}
	case "darwin":
		candidates := []string{
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
		}
	default:
		for _, name := range []string{"chromium-browser", "chromium", "google-chrome", "google-chrome-stable", "microsoft-edge"} {
			if p, err := exec.LookPath(name); err == nil {
				return p, nil
			}
		}
	}
	return "", nil
}

// hScreenshot — снимок страницы через headless-браузер.
func (r *Registry) hScreenshot(ctx context.Context, m map[string]any) (Result, error) {
	target := strings.TrimSpace(ArgStr(m, "target"))
	if target == "" {
		return Result{}, fmt.Errorf("укажи target — URL или путь к HTML-файлу")
	}
	browser, _ := findBrowser()

	// Локальный файл → file:// URL.
	if !strings.Contains(target, "://") {
		p, err := r.pathArg(target)
		if err != nil {
			return Result{}, err
		}
		if _, err := os.Stat(p); err != nil {
			return Result{}, fmt.Errorf("файл не найден: %s", p)
		}
		target = fileURL(p)
	} else {
		// Сетевой адрес браузер качает сам — та же дыра, что закрыта у
		// web_fetch: без проверки скриншот http://169.254.169.254/ приносил
		// бы ответ внутренней сети прямо в контекст модели.
		if err := checkSSRF(target); err != nil {
			return Result{}, err
		}
	}

	w := ArgInt(m, "width", 1440)
	h := ArgInt(m, "height", 900)
	w = core.Clamp(w, 240, 3840)
	h = core.Clamp(h, 240, 3840)
	// Вся страница: ширина та же, высота окна — большая. Иначе низ страницы
	// виден только по кускам, и каждый кусок — отдельный запрос со своим
	// снимком в контексте.
	if ArgBool(m, "full_page") {
		h = fullPageHeight
	}

	// Куда сохранить: .gcli/screenshots/<имя>.png или как попросили.
	out := strings.TrimSpace(ArgStr(m, "out"))
	if out == "" {
		out = filepath.Join(".gcli", "screenshots", "shot-"+core.RandID(5)+".png")
	}
	// pathArg, а не resolvePath: путь пришёл от модели, и песочница
	// обязана его резать так же, как у write_file — иначе screenshot
	// стал бы бесплатной записью куда угодно (и MkdirAll создавал бы
	// каталоги вне корня).
	outPath, err := r.pathArg(out)
	if err != nil {
		return Result{}, err
	}
	if filepath.Ext(outPath) == "" {
		outPath += ".png"
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return Result{}, fmt.Errorf("не удалось создать каталог для скриншота: %v", err)
	}

	if browser == "" {
		return Result{}, fmt.Errorf(
			"headless-браузер не найден (нужен Edge, Chrome или Chromium). " +
				"Установи любой из них или укажи путь в переменной GCLI_BROWSER")
	}

	// Отдельный профиль: не трогаем браузер пользователя и не падаем,
	// если он уже запущен.
	profile := filepath.Join(os.TempDir(), "gcli-browser-"+core.RandID(6))
	defer os.RemoveAll(profile)

	args := []string{
		"--headless",
		"--disable-gpu",
		"--no-first-run",
		"--disable-extensions",
		"--hide-scrollbars",
		"--force-device-scale-factor=1",
		"--virtual-time-budget=4000",
		// Классический краш Chromium в контейнерах и на CI-раннерах
		// (signal: aborted, core dumped): /dev/shm слишком мал для
		// его IPC-разделяемой памяти. Флаг переводит её в /tmp —
		// медленнее, но снимок не падает.
		"--disable-dev-shm-usage",
		// Лог браузера в stderr: оттуда берутся ошибки консоли страницы.
		// Без этого агент видит снимок красивого экрана и не знает, что
		// скрипт на странице падает.
		"--enable-logging=stderr",
		"--v=1",
		"--user-data-dir=" + profile,
		fmt.Sprintf("--window-size=%d,%d", w, h),
		"--screenshot=" + outPath,
		target,
	}
	// Песочница Chromium не работает под root и в контейнерах без
	// user-namespace: браузер падает до отрисовки. В обычном запуске
	// от root это тоже спасает, а на пользовательской машине флаг не
	// нужен — включаем только там, где без него краш.
	if browserNeedsNoSandbox() {
		args = append(args, "--no-sandbox")
	}

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, browser, args...)
	cmd.Env = append(os.Environ(), "HOME="+os.TempDir())
	// Браузер уходит в свою группу: по таймауту добиваем всё дерево
	// (дети Chromium переживают бы прямой Kill).
	PGroup(cmd)
	cmd.Cancel = func() error { return KillTree(cmd) }
	cmd.WaitDelay = 2 * time.Second
	// Лог браузера — через Capture: с --v=1 Chromium на тяжёлой странице
	// выдаёт десятки мегабайт. Окно большое (по 1 МБ), чтобы ошибки
	// консоли страницы не потерялись в середине.
	logCap := core.NewCapture(1024 * 1024)
	cmd.Stdout = logCap
	cmd.Stderr = logCap
	err = cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		// Браузер вышел, а фоновый потомок держит вывод открытым:
		// скриншот уже записан — не заваливаем результат.
		err = nil
	}
	if err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return Result{}, fmt.Errorf("браузер не успел за 60 с — страница, вероятно, слишком тяжёлая")
		}
		// Хвост лога — в ошибку: без него краш браузера («signal: aborted»)
		// нечитаем, и отличить нехватку памяти от сломанного профиля
		// можно было только повторным запуском руками.
		return Result{}, fmt.Errorf("скриншот не удался: %v\n%s", err, core.TruncateUTF8(logCap.String(), 600, 600))
	}
	res, err := r.attachImageFile(outPath, "screenshot")
	if err != nil {
		return res, err
	}
	// Ошибки консоли страницы — в текст результата: агент читает текст
	// результата, а картинку он смотрит глазами.
	if errs := consoleErrors(logCap.String()); len(errs) > 0 {
		res.Text += "\n\nОшибки в консоли страницы:\n" + strings.Join(errs, "\n")
		res.Summary += " · ошибок в консоли: " + strconv.Itoa(len(errs))
	}
	return res, nil
}

// consoleErrors — ошибки консоли страницы из лога браузера.
//
// Chromium пишет их в stderr строкой вида
// «[INFO:CONSOLE(12)] "Uncaught TypeError: x", source: file:///…».
// Необработанное исключение помечается INFO, а console.error — ERROR, так
// что ловим оба.
//
// Оставляем только сообщения самой страницы: у них источник — её адрес
// (file:// или http://). Всё остальное (расширения браузера, его
// внутренние модули) — шум, и в контекст модели он не нужен. Из
// сообщений страницы берём только ошибки: обычный console.log — это не
// проблема.
func consoleErrors(log string) []string {
	var out []string
	for _, line := range strings.Split(log, "\n") {
		i := strings.Index(line, "CONSOLE")
		if i < 0 {
			continue
		}
		prefix := line[:i]
		rest := line[i+len("CONSOLE"):]
		// Отрезаем номер строки: "(12)] ".
		j := strings.Index(rest, "]")
		if j < 0 {
			continue
		}
		rest = strings.TrimSpace(rest[j+1:])
		if rest == "" {
			continue
		}
		// Источник: страница или расширение браузера.
		src := ""
		if k := strings.Index(rest, ", source:"); k >= 0 {
			src = strings.TrimSpace(rest[k+len(", source:"):])
			rest = strings.TrimSpace(rest[:k])
		}
		if !strings.HasPrefix(src, "file://") && !strings.HasPrefix(src, "http") {
			continue
		}
		msg := strings.Trim(strings.TrimSpace(rest), `"`)
		if msg == "" {
			continue
		}
		// Только ошибки: console.error и необработанные исключения.
		isErr := strings.Contains(prefix, "ERROR:") ||
			strings.HasPrefix(msg, "Uncaught ") ||
			strings.HasPrefix(msg, "TypeError") ||
			strings.HasPrefix(msg, "ReferenceError") ||
			strings.HasPrefix(msg, "SyntaxError")
		if !isErr {
			continue
		}
		if len([]rune(msg)) > 200 {
			msg = string([]rune(msg)[:200]) + "…"
		}
		out = append(out, "• "+msg)
		if len(out) >= 10 {
			break
		}
	}
	return out
}

// hReadImage — приложить локальное изображение к контексту.
func (r *Registry) hReadImage(_ context.Context, m map[string]any) (Result, error) {
	p := strings.TrimSpace(ArgStr(m, "path"))
	if p == "" {
		return Result{}, fmt.Errorf("укажи path — путь к изображению")
	}
	abs, err := r.pathArg(p)
	if err != nil {
		return Result{}, err
	}
	return r.attachImageFile(abs, "read_image")
}

// fileURL — file://-адрес локального пути.
//
// Склейка «file://» + путь на Windows даёт «file://C:\x»: буква диска
// уезжает в Host, и браузер файл не находит. Нужен «file:///C:/x».
func fileURL(p string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(p)}
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}
	return u.String()
}

// attachImageFile — прочитать файл изображения и приложить к результату.
func (r *Registry) attachImageFile(path, tool string) (Result, error) {
	// Размер проверяем до чтения: иначе файл на гигабайт сначала целиком
	// попадал в память и только потом отвергался.
	st, err := os.Stat(path)
	if err != nil {
		return Result{}, fmt.Errorf("не удалось прочитать изображение: %v", err)
	}
	if st.IsDir() {
		return Result{}, fmt.Errorf("это каталог, а не изображение: %s", path)
	}
	if st.Size() > maxImageBytes {
		return Result{}, fmt.Errorf(
			"изображение слишком большое (%s, потолок 4 МБ) — уменьши его и повтори",
			core.HumanSize(int(st.Size())))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("не удалось прочитать изображение: %v", err)
	}
	// Файл мог вырасти между Stat и ReadFile.
	if len(data) > maxImageBytes {
		return Result{}, fmt.Errorf(
			"изображение слишком большое (%s, потолок 4 МБ) — уменьши его и повтори",
			core.HumanSize(len(data)))
	}
	mime := mimeByExt[strings.ToLower(filepath.Ext(path))]
	if mime == "" {
		// Неизвестное расширение: пробуем угадать по сигнатуре.
		switch {
		case len(data) > 8 && data[0] == 0x89 && data[1] == 'P':
			mime = "image/png"
		case len(data) > 3 && data[0] == 0xFF && data[1] == 0xD8:
			mime = "image/jpeg"
		case len(data) > 12 && string(data[8:12]) == "WEBP":
			mime = "image/webp"
		case len(data) > 6 && string(data[0:6]) == "GIF89a" || len(data) > 6 && string(data[0:6]) == "GIF87a":
			mime = "image/gif"
		default:
			return Result{}, fmt.Errorf("неизвестный формат изображения (нужен png, jpg, webp или gif): %s", filepath.Base(path))
		}
	}
	img := core.Image{
		MIME: mime,
		Data: base64.StdEncoding.EncodeToString(data),
		Path: path,
	}
	// Подпись для текстового контекста: модель видит и картинку, и факт,
	// что она её видит.
	label := filepath.Base(path)
	return Result{
		Text: fmt.Sprintf(
			"Изображение приложено к контексту: %s (%s, %s). Рассмотри его: выше в диалоге идёт картинка.",
			label, core.HumanSize(len(data)), mime),
		Summary: fmt.Sprintf("%s: %s", tool, label),
		Images:  []core.Image{img},
	}, nil
}
