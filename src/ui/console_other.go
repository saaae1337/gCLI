//go:build !windows

package ui

import "os"

// enableVT — на POSIX escape-последовательности поддерживаются всегда.
func EnableVT() bool { return true }

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
