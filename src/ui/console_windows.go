//go:build windows

package ui

import (
	"os"
	"syscall"
	"unsafe"
)

// Работа с консолью Windows: включение ANSI (VT) и получение размеров окна.

var (
	modkernel32         = syscall.NewLazyDLL("kernel32.dll")
	procGetStdHandle    = modkernel32.NewProc("GetStdHandle")
	procGetConsoleMode  = modkernel32.NewProc("GetConsoleMode")
	procSetConsoleMode  = modkernel32.NewProc("SetConsoleMode")
	procGetConsoleSInfo = modkernel32.NewProc("GetConsoleScreenBufferInfo")
	procGetConsoleCP    = modkernel32.NewProc("GetConsoleCP")
	procSetConsoleCP    = modkernel32.NewProc("SetConsoleCP")
	procGetConsoleOCP   = modkernel32.NewProc("GetConsoleOutputCP")
	procSetConsoleOCP   = modkernel32.NewProc("SetConsoleOutputCP")
)

const (
	stdOutputHandle              = ^uintptr(10) // (DWORD)-11
	enableVirtualTerminalProcess = 0x0004
)

// consoleOutputCP — текущая кодовая страница вывода, 0 если консоли нет.
func consoleOutputCP() int {
	if r, _, _ := procGetConsoleOCP.Call(); r != 0 {
		return int(r)
	}
	return 0
}

// switchConsoleToUTF8 — перевести консоль в UTF-8, если она ещё не там.
//
// Возвращает, удалось ли перевести вывод. Ввод переводится попутно и на
// ответ не влияет: печатать псевдографику можно и тогда, когда читать
// кириллицу с клавиатуры не вышло.
//
// Отказ — норма, а не авария: прав на смену кодовой страницы может не
// быть, и тогда остаётся ASCII-режим вместо нечитаемых символов.
func switchConsoleToUTF8() bool {
	// Ввод: чтобы кириллица с клавиатуры не превращалась в мусор.
	if r, _, _ := procGetConsoleCP.Call(); r != 0 && r != uintptr(cpUTF8) {
		procSetConsoleCP.Call(uintptr(cpUTF8))
	}
	out := consoleOutputCP()
	if out == 0 || out == cpUTF8 {
		return true
	}
	if r, _, _ := procSetConsoleOCP.Call(uintptr(cpUTF8)); r != 0 {
		return true
	}
	return false
}

// ConsoleUTF8 — можно ли печатать псевдографику в эту консоль.
//
// tty=false — вывод ушёл в файл или пайп. Тогда UTF-8 всё равно
// правильный ответ: Go пишет UTF-8 независимо от консоли, и
// переключать кодовую страницу пользователя незачем, он этого не просил.
// Это не мелочь: «gcli -json > отчёт.json» не должен переводить окно
// терминала.
//
// Пытается перевести консоль в UTF-8 и только потом отвечает. Смысл: у
// пользователя не должно быть способа случайно получить «тФВ» вместо
// «│». Если перевести не вышло — честно откатываемся на ASCII.
func ConsoleUTF8(tty bool) bool {
	if !tty {
		return true
	}
	return consoleCanShowUnicode(consoleOutputCP(), switchConsoleToUTF8())
}

// EnableVT — включить обработку ANSI-последовательностей для stdout.
// Возвращает true, если escape-коды поддерживаются.
func EnableVT() bool {
	h, _, _ := procGetStdHandle.Call(stdOutputHandle)
	if h == 0 || h == ^uintptr(0) {
		return false
	}
	var mode uint32
	r, _, _ := procGetConsoleMode.Call(h, uintptr(unsafe.Pointer(&mode)))
	if r == 0 {
		return false
	}
	mode |= enableVirtualTerminalProcess
	r, _, _ = procSetConsoleMode.Call(h, uintptr(mode))
	return r != 0
}

type coord struct{ X, Y int16 }
type smallRect struct{ Left, Top, Right, Bottom int16 }
type consoleScreenBufferInfo struct {
	Size              coord
	CursorPosition    coord
	Attributes        uint16
	Window            smallRect
	MaximumWindowSize coord
}

// terminalWidth — ширина терминала в колонках.
func terminalWidth() int {
	h, _, _ := procGetStdHandle.Call(stdOutputHandle)
	if h == 0 || h == ^uintptr(0) {
		return 0
	}
	var info consoleScreenBufferInfo
	r, _, _ := procGetConsoleSInfo.Call(h, uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 0
	}
	w := int(info.Window.Right-info.Window.Left) + 1
	if w < 20 {
		return 0
	}
	return w
}

// isTerminal — является ли stdout терминалом.
func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
