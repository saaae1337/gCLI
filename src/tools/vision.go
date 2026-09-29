package tools

import (
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

// ---------- Зрение агента: скриншоты и просмотр изображений ----------
//
// У агента нет глаз: сверстанный сайт он видит только как код. Инструменты
// ниже дают ему зрение:
//
//	screenshot — снимок страницы (URL или локальный файл) через headless-
//	             браузер (Edge/Chrome/Chromium): PNG прикладывается к
//	             контексту, модель видит картинку на следующем шаге;
//	read_image — чтение локального изображения (PNG/JPG/WebP/GIF) в контекст.
//
// Картинки уходят в API как обычные image-блоки (OpenAI: image_url с
// data-URL, Anthropic: блок image с base64-источником).

// maxImageBytes — потолок размера исходного файла изображения (~5 МБ base64
// у Anthropic, у OpenAI похоже; берём с запасом меньше лимита API).
const maxImageBytes = 4 * 1024 * 1024

// maxImagesPerResult — сколько изображений один инструмент может приложить.
const maxImagesPerResult = 4

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
	`"width":{"type":"integer","description":"Ширина вьюпорта, px (по умолчанию 1440)"},` +
	`"height":{"type":"integer","description":"Высота вьюпорта, px (по умолчанию 900)"}` +
	`},"required":["target"]}`

const schemaReadImage = `{"type":"object","properties":{` +
	`"path":{"type":"string","description":"Путь к изображению (png, jpg, webp, gif)"}` +
	`},"required":["path"]}`

// RegisterVision — инструменты зрения.
func (r *Registry) RegisterVision() {
	r.registerBound("screenshot", "Сделать скриншот страницы и ПОСМОТРЕТЬ на него глазами: снимок прикладывается к твоему контексту как изображение. "+
		"Используй после вёрстки/изменения UI (target — URL или HTML-файл), чтобы проверить, как страница выглядит на самом деле.",
		schemaScreenshot, "read", false, func(r *Registry) Handler { return r.hScreenshot })
	r.registerBound("read_image", "Посмотреть на изображение с диска (png, jpg, webp, gif): файл прикладывается к контексту как изображение. "+
		"Используй для макетов, скриншотов пользователя, схем.",
		schemaReadImage, "read", false, func(r *Registry) Handler { return r.hReadImage })
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
	if browser == "" {
		return Result{}, fmt.Errorf(
			"headless-браузер не найден (нужен Edge, Chrome или Chromium). " +
				"Установи любой из них или укажи путь в переменной GCLI_BROWSER")
	}

	// Локальный файл → file:// URL.
	if !strings.Contains(target, "://") {
		p := r.resolvePath(target)
		if _, err := os.Stat(p); err != nil {
			return Result{}, fmt.Errorf("файл не найден: %s", p)
		}
		target = "file://" + p
		if runtime.GOOS == "windows" {
			target = strings.ReplaceAll(target, "\\", "/")
		}
	}

	w := ArgInt(m, "width", 1440)
	h := ArgInt(m, "height", 900)
	w = core.Clamp(w, 240, 3840)
	h = core.Clamp(h, 240, 3840)

	// Куда сохранить: .gcli/screenshots/<имя>.png или как попросили.
	out := strings.TrimSpace(ArgStr(m, "out"))
	if out == "" {
		out = filepath.Join(".gcli", "screenshots", "shot-"+core.RandID(5)+".png")
	}
	outPath := r.resolvePath(out)
	if filepath.Ext(outPath) == "" {
		outPath += ".png"
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return Result{}, fmt.Errorf("не удалось создать каталог для скриншота: %v", err)
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
		"--user-data-dir=" + profile,
		fmt.Sprintf("--window-size=%d,%d", w, h),
		"--screenshot=" + outPath,
		target,
	}

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, browser, args...)
	cmd.Env = append(os.Environ(), "HOME="+os.TempDir())
	_, err := cmd.CombinedOutput()
	if cctx.Err() == context.DeadlineExceeded {
		return Result{}, fmt.Errorf("браузер не успел за 60 с — страница, вероятно, слишком тяжёлая")
	}
	if err != nil {
		return Result{}, fmt.Errorf("скриншот не удался: %v", err)
	}
	return r.attachImageFile(outPath, "screenshot")
}

// hReadImage — приложить локальное изображение к контексту.
func (r *Registry) hReadImage(_ context.Context, m map[string]any) (Result, error) {
	p := strings.TrimSpace(ArgStr(m, "path"))
	if p == "" {
		return Result{}, fmt.Errorf("укажи path — путь к изображению")
	}
	return r.attachImageFile(r.resolvePath(p), "read_image")
}

// attachImageFile — прочитать файл изображения и приложить к результату.
func (r *Registry) attachImageFile(path, tool string) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("не удалось прочитать изображение: %v", err)
	}
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

// ClampImages — ограничить список изображений (потолок на один результат).
func ClampImages(imgs []core.Image) []core.Image {
	if len(imgs) > maxImagesPerResult {
		return imgs[:maxImagesPerResult]
	}
	return imgs
}
