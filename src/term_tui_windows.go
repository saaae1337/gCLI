//go:build windows

package main

import (
	"unsafe"
)

// Полноэкранный режим для нового интерфейса.
//
// Отличия от makeRawStdin (построчный REPL): Ctrl+C должен приходить
// клавишей, а не сигналом (иначе его не отличить от аварийного выхода и
// нельзя отдать полю ввода), а Enter — приходить как CR, чтобы отделить
// его от Ctrl+J. Поэтому ENABLE_PROCESSED_INPUT здесь выключается.

var procGetConsoleSInfoTUI = kernel32.NewProc("GetConsoleScreenBufferInfo")

type winCoord struct{ X, Y int16 }

type winRect struct{ Left, Top, Right, Bottom int16 }

type winConsoleInfo struct {
	Size              winCoord
	CursorPosition    winCoord
	Attributes        uint16
	Window            winRect
	MaximumWindowSize winCoord
}

const (
	cStdOutputHandle     = ^uintptr(11) // (DWORD)-11
	cEnableExtendedFlags = 0x0080
	cEnableQuickEdit     = 0x0040
)

// makeRawTUI — raw-режим для полноэкранного интерфейса.
func makeRawTUI() (restore func(), ok bool) {
	h, _, _ := procGetStdHandleIn.Call(stdInputHandle)
	if h == 0 || h == ^uintptr(0) {
		return nil, false
	}
	var mode uint32
	if r, _, _ := procGetConsoleMode.Call(h, uintptr(unsafe.Pointer(&mode))); r == 0 {
		return nil, false
	}
	next := mode &^ (cEnableLineInput | cEnableEchoInput | cEnableProcessedInput)
	// 0x0080 обязателен, чтобы менять QuickEdit; сам QuickEdit выключаем:
	// иначе выделение мышью замораживает вывод консоли до нажатия клавиши.
	// ENABLE_WINDOW_INPUT не включаем: размер окна опрашивается по таймеру,
	// а события изменения размера в поток ReadFile не нужны.
	next = next&^cEnableQuickEdit | cEnableExtendedFlags
	next |= cEnableVirtualTerminInput
	if r, _, _ := procSetConsoleMode.Call(h, uintptr(next)); r == 0 {
		return nil, false
	}
	return func() {
		procSetConsoleMode.Call(h, uintptr(mode))
	}, true
}

// termSize — размер окна консоли в клетках.
func termSize() (w, h int) {
	hOut, _, _ := procGetStdHandleIn.Call(cStdOutputHandle)
	if hOut == 0 || hOut == ^uintptr(0) {
		return 0, 0
	}
	var info winConsoleInfo
	if r, _, _ := procGetConsoleSInfoTUI.Call(hOut, uintptr(unsafe.Pointer(&info))); r == 0 {
		return 0, 0
	}
	w = int(info.Window.Right-info.Window.Left) + 1
	h = int(info.Window.Bottom-info.Window.Top) + 1
	if w < 1 || h < 1 {
		return 0, 0
	}
	return w, h
}

// stdinIsTerminal — подключён ли ввод к консоли.
func stdinIsTerminal() bool {
	h, _, _ := procGetStdHandleIn.Call(stdInputHandle)
	if h == 0 || h == ^uintptr(0) {
		return false
	}
	var mode uint32
	r, _, _ := procGetConsoleMode.Call(h, uintptr(unsafe.Pointer(&mode)))
	return r != 0
}
