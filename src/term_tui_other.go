//go:build !linux && !darwin && !windows

package main

// На прочих платформах полноэкранный режим не реализован: остаётся
// обычный построчный интерфейс.
func makeRawTUI() (restore func(), ok bool) { return nil, false }

func termSize() (w, h int) { return 0, 0 }

func stdinIsTerminal() bool { return false }

// rawTUIHint — на прочих платформах подсказки нет: режим не реализован.
func rawTUIHint() string { return "" }
