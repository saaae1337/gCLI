//go:build !linux && !darwin && !windows

package main

// На прочих платформах raw-режим stdin не реализован: горячие клавиши
// работают так, как их отдаёт терминал (то есть после Enter). Это заглушка
// ради сборки: план9/js всё равно не целевые платформы gcli.
func makeRawStdin() (restore func(), ok bool) { return nil, false }
