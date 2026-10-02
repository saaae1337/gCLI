//go:build linux

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// Raw-режим stdin на Linux.
//
// Зачем это всё: gcli читает ввод в canonical-режиме, и тот живёт не у нас,
// а в line discipline терминала. Дисциплина перехватывает часть байтов, не
// отдавая их приложению: Ctrl+O — это VDISCARD («прекратить вывод», при
// включённом IEXTEN вообще не доходит до программы), Ctrl+S/Ctrl+Q —
// программный flow control (IXON замораживает вывод), стрелки копятся в
// строке как мусор. Отсюда и баг «Ctrl+O не работает»: байт 0x0F просто
// не доходит до watchKeys.
//
// Мы не уводим терминал в полный raw (как это делают TUI-фреймворки):
// нужен построчный ввод, а своё полноценное line-editing тащить рано.
// Точечные изменения:
//
//	ICANON off — байты приходят сразу, без ожидания Enter;
//	ECHO off   — эхо печатаем сами (иначе raw-байты дублируются);
//	IEXTEN off — VDISCARD/VWERASE/VLNEXT больше не перехватываются;
//	IXON  off  — Ctrl+S не замораживает вывод;
//	ISIG on    — Ctrl+C по-прежнему сигнал, как и раньше;
//	ICRNL on   — Enter приходит как '\n', разбор строк не меняется;
//	VMIN=1     — чтение по одному байту без таймаута.
//
// Побочный эффект ICANON off: стирание строки (Backspace, Ctrl+U) теперь
// делает stdinReader (см. input.go), а не дисциплина.
func makeRawStdin() (restore func(), ok bool) {
	fd := int(os.Stdin.Fd())
	var old syscall.Termios
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&old)), 0, 0, 0); errno != 0 {
		// Не терминал (пайп, файл): raw не нужен, ввод работает как раньше.
		return nil, false
	}

	raw := old
	raw.Iflag &^= syscall.IXON
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN
	raw.Lflag |= syscall.ISIG
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0

	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&raw)), 0, 0, 0); errno != 0 {
		return nil, false
	}
	return func() {
		syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd),
			uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&old)), 0, 0, 0)
	}, true
}
