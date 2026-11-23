//go:build !windows

package ui

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// atoiSafe - безопасно распарсить число.
func atoiSafe(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}

// enableVT - на POSIX escape-последовательности поддерживаются всегда.
func EnableVT() bool { return true }

// ConsoleUTF8 - на POSIX решение принимает локаль, а не консоль:
// переключать кодовую страницу нечем и незачем. Здесь всегда «умеет»,
// а отсекает юникод проверка LANG/LC_ALL в main.
func ConsoleUTF8(tty bool) bool { return true }

// terminalSize - текущий размер окна терминала (колонки, строки).
//
// Сначала ioctl TIOCGWINSZ по stdout - единственный источник правды о
// реальном размере окна. Переменные COLUMNS/LINES - запасной путь для
// сред без ioctl (переадресованный вывод под ssh, некоторые pty-обёртки):
// шеллы их обычно не экспортируют, но если выставлены - доверять можно.
// Ноль в поле означает «неизвестно».
//
// До этого функция читала только COLUMNS: на linux/macOS шеллы не
// экспортируют её, интерфейс жил с шириной по умолчанию и не замечал
// изменения размера окна - «не масштабируется».
func terminalSize() (w, h int) {
	fd := int(os.Stdout.Fd())
	var ws struct{ Row, Col, X, Y uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno == 0 && ws.Col > 0 && ws.Row > 0 {
		return int(ws.Col), int(ws.Row)
	}
	if s := os.Getenv("COLUMNS"); s != "" {
		if n := atoiSafe(s, 0); n > 20 {
			w = n
		}
	}
	if s := os.Getenv("LINES"); s != "" {
		h = atoiSafe(s, 0)
	}
	return w, h
}

// terminalWidth - ширина терминала.
func terminalWidth() int {
	w, _ := terminalSize()
	return w
}

// isTerminal - является ли stdout терминалом.
func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
