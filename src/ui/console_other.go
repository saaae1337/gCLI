//go:build !windows

package ui

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
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

// terminalWidth — ширина терминала. Сначала ioctl TIOCGWINSZ по
// stdout/stderr/stdin: COLUMNS экспортируют далеко не все эмуляторы
// (Terminal.app, iTerm2, xterm), и без ioctl не-дашбордные панели
// верстались под дефолтные 100 колонок в 80-колоночном терминале.
// COLUMNS остаётся запасным путём, 0 — «взять дефолт».
func terminalWidth() int {
	var ws struct{ Row, Col, Xpixel, Ypixel uint16 }
	for _, fd := range []uintptr{os.Stdout.Fd(), os.Stderr.Fd(), os.Stdin.Fd()} {
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd,
			uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws))); errno == 0 && ws.Col > 20 {
			return int(ws.Col)
		}
	}
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
