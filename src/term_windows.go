//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// Raw-режим stdin на Windows.
//
// Консоль Windows по умолчанию работает в строчном режиме
// (ENABLE_LINE_INPUT): ReadFile отдаёт текст только после Enter, а Ctrl+O
// просто копится в буфере строки. Для горячих клавиш выключаем LINE и ECHO
// (эхо печатает stdinReader), включаем VT-ввод — стрелки приходят теми же
// ESC-последовательностями, что и на Unix, и глотаются одним и тем же
// фильтром. ENABLE_PROCESSED_INPUT оставляем: Ctrl+C — по-прежнему сигнал.
var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetStdHandleIn = kernel32.NewProc("GetStdHandle")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
)

const (
	stdInputHandle           = ^uintptr(10) // (DWORD)-10
	cEnableProcessedInput    = 0x0001
	cEnableLineInput         = 0x0002
	cEnableEchoInput         = 0x0004
	cEnableWindowInput       = 0x0008
	cEnableVirtualTerminInput = 0x0200
)

func makeRawStdin() (restore func(), ok bool) {
	h, _, _ := procGetStdHandleIn.Call(stdInputHandle)
	if h == 0 || h == ^uintptr(0) {
		return nil, false
	}
	var mode uint32
	if r, _, _ := procGetConsoleMode.Call(h, uintptr(unsafe.Pointer(&mode))); r == 0 {
		return nil, false // не консоль (пайп): raw не нужен
	}
	next := mode &^ (cEnableLineInput | cEnableEchoInput)
	next |= cEnableProcessedInput | cEnableWindowInput | cEnableVirtualTerminInput
	if r, _, _ := procSetConsoleMode.Call(h, uintptr(next)); r == 0 {
		return nil, false
	}
	return func() {
		procSetConsoleMode.Call(h, uintptr(mode))
	}, true
}
