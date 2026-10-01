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
)

const (
	stdOutputHandle              = ^uintptr(10) // (DWORD)-11
	enableVirtualTerminalProcess = 0x0004
)

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
