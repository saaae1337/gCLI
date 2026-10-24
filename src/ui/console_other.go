//go:build !windows

package ui

import (
	"os"
	"strconv"
	"strings"
)

// atoiSafe — безопасно распарсить число.
func atoiSafe(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}

// enableVT — на POSIX escape-последовательности поддерживаются всегда.
func EnableVT() bool { return true }

// ConsoleUTF8 — на POSIX решение принимает локаль, а не консоль:
// переключать кодовую страницу нечем и незачем. Здесь всегда «умеет»,
// а отсекает юникод проверка LANG/LC_ALL в main.
func ConsoleUTF8(tty bool) bool { return true }

// terminalWidth — ширина терминала через переменные COLUMNS или ioctl не делаем:
// достаточно читать COLUMNS, иначе возвращаем 0 (взять дефолт).
func terminalWidth() int {
	if s := os.Getenv("COLUMNS"); s != "" {
		if n := atoiSafe(s, 0); n > 20 {
			return n
		}
	}
	return 0
}

// isTerminal — является ли stdout терминалом.
func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
