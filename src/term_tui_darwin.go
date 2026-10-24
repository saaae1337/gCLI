//go:build darwin

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// Полноэкранный режим для нового интерфейса (macOS).
//
// То же, что на Linux, но ioctl называются TIOCGETA/TIOCSETA: BSD-наследие
// macOS. ISIG выключен — Ctrl+C обрабатывает интерфейс, а не сигнал.

func makeRawTUI() (restore func(), ok bool) {
	fd := int(os.Stdin.Fd())
	var old syscall.Termios
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(syscall.TIOCGETA), uintptr(unsafe.Pointer(&old)), 0, 0, 0); errno != 0 {
		return nil, false
	}
	raw := old
	raw.Iflag &^= syscall.IXON | syscall.ICRNL
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN | syscall.ISIG
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(syscall.TIOCSETA), uintptr(unsafe.Pointer(&raw)), 0, 0, 0); errno != 0 {
		return nil, false
	}
	return func() {
		syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd),
			uintptr(syscall.TIOCSETA), uintptr(unsafe.Pointer(&old)), 0, 0, 0)
	}, true
}

// termSize — размер окна терминала.
func termSize() (w, h int) {
	var ws struct {
		Row, Col, Xpixel, Ypixel uint16
	}
	fd := int(os.Stdout.Fd())
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)), 0, 0, 0)
	if errno != 0 || ws.Col == 0 || ws.Row == 0 {
		return 0, 0
	}
	return int(ws.Col), int(ws.Row)
}

// stdinIsTerminal — подключён ли ввод к терминалу.
func stdinIsTerminal() bool {
	fd := int(os.Stdin.Fd())
	var t syscall.Termios
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(syscall.TIOCGETA), uintptr(unsafe.Pointer(&t)), 0, 0, 0)
	return errno == 0
}

// rawTUIHint — подсказка к отказу raw-режима на macOS.
func rawTUIHint() string {
	if !stdinIsTerminal() {
		return " · stdin не терминал (пайп?) — полноэкранному режиму нужен настоящий tty"
	}
	return ""
}
