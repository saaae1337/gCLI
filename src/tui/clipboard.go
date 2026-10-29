package tui

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ---------- Буфер обмена ----------
//
// В терминале у приложения нет прямого доступа к системному буферу:
// запись делается последовательностью OSC52 (её подхватывают современные
// терминалы, включая внутренние буферы SSH), чтение — штатной утилитой
// платформы. Оба пути завёрнуты сюда, чтобы остальной код не знал
// деталей.

// OSC52 — последовательность «положи текст в системный буфер обмена».
func OSC52(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
}

// clipboardCandidates — утилиты чтения буфера в порядке предпочтения.
// Проверяются переменные окружения, чтобы не звать заведомо мёртвый
// бинарник: под Wayland xclip без X-моста бесполезен, и наоборот.
func clipboardCandidates() [][]string {
	switch runtime.GOOS {
	case "darwin":
		return [][]string{{"pbpaste"}}
	case "windows":
		return [][]string{
			{"powershell", "-NoProfile", "-Command", "Get-Clipboard"},
		}
	}
	var out [][]string
	wayland := os.Getenv("WAYLAND_DISPLAY") != ""
	x11 := os.Getenv("DISPLAY") != ""
	if wayland {
		out = append(out, []string{"wl-paste", "--no-newline"})
	}
	if x11 || wayland {
		out = append(out, []string{"xclip", "-selection", "clipboard", "-o"})
	}
	if x11 {
		out = append(out, []string{"xsel", "--clipboard", "--output"})
	}
	out = append(out, []string{"termux-clipboard-get"})
	return out
}

// ReadClipboard — прочитать системный буфер обмена. Управляющие символы
// вычищаются тем же санитайзером, что и вставка из терминала: мусор из
// буфера не должен попадать в поле ввода. Окно ожидания короткое: утилита
// либо отвечает мгновенно, либо её нет — интерфейс ждёт не будет.
func ReadClipboard() (string, bool) {
	for _, c := range clipboardCandidates() {
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		out, err := exec.CommandContext(ctx, c[0], c[1:]...).Output()
		cancel()
		if err != nil {
			continue
		}
		if s := sanitizeInput(strings.TrimRight(string(out), "\n")); s != "" {
			return s, true
		}
	}
	return "", false
}
