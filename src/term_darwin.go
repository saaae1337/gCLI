//go:build darwin

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// Raw-режим stdin на macOS: то же, что и на Linux (см. term_linux.go) —
// canonical-режим терминала перехватывает Ctrl+O (VDISCARD) и Ctrl+S/Q
// (IXON), поэтому для горячих клавиш нужен точечный raw.
//
// Отличие от Linux только в номерах ioctl: BSD-наследие macOS зовёт их
// TIOCGETA/TIOCSETA вместо TCGETS/TCSETS. ISIG остаётся включённым —
// Ctrl+C работает как раньше, ICRNL не трогаем — Enter приходит как '\n'.
func makeRawStdin() (restore func(), ok bool) {
	fd := int(os.Stdin.Fd())
	var old syscall.Termios
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(syscall.TIOCGETA), uintptr(unsafe.Pointer(&old)), 0, 0, 0); errno != 0 {
		return nil, false
	}

	raw := old
	raw.Iflag &^= syscall.IXON
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN
	raw.Lflag |= syscall.ISIG
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
