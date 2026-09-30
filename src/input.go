package main

import (
	"io"
	"os"
	"strings"
)

// ---------- Ввод: один владелец stdin ----------
//
// Читать stdin из двух мест нельзя: буфер bufio.Scanner и отдельный
// обработчик Ctrl+O разъедали бы друг другу байты (в paste-буфере это
// выглядело бы как «пропала первая буква запроса»). Поэтому stdin читает
// ровно одна горутина — stdinReader, — а все остальные берут готовое из
// каналов. Побочный эффект приятный: работает одинаково и в Windows
// Terminal, и в mintty, без одной платформенной строки.
//
// Канал lines отдаёт готовые строки (без перевода строки), keys —
// горячие клавиши. Всё остальное, включая escape-последовательности
// стрелок, считается обычным текстом строки: так же было и раньше, когда
// строки читались сканером.

const ctrlO = 0x0F // ^O

// stdinReader — построчный ввод для REPL и диалогов.
type stdinReader struct {
	lines <-chan string
	keys  <-chan byte
	cur   string
	ok    bool
}

// Scan — прочитать следующую строку (как у bufio.Scanner).
func (s *stdinReader) Scan() bool {
	s.cur, s.ok = <-s.lines
	return s.ok
}

// Text — последняя прочитанная строка.
func (s *stdinReader) Text() string { return s.cur }

// Keys — канал горячих клавиш. Пока ход идёт, его читает turnWatcher;
// в остальное время он копит управляющие байты, чтобы Ctrl+C и прочее
// не потерялись (в cook-режиме терминала это не страшно, но при выходе
// из REPL неожиданно «съеденная» клавиша выглядела бы как баг).
func (s *stdinReader) Keys() <-chan byte { return s.keys }

// newStdin — запустить фоновый ридер и вернуть обёртку.
//
// Единственный отказ — прочитать нечего (закрытый или перенаправленный
// ввод). В этом случае REPL сразу увидит EOF и попрощается.
func newStdin() *stdinReader {
	lines := make(chan string, 8)
	keys := make(chan byte, 8)

	go func() {
		defer close(lines)
		defer close(keys)
		in := os.Stdin
		var part []byte
		buf := make([]byte, 4096)
		for {
			n, err := in.Read(buf)
			for _, b := range buf[:n] {
				switch {
				case b == ctrlO:
					// Горячая клавиша: в строку ввода не попадает.
					// Канал буферизован и не блокируется, но если
					// пользователь залипает на Ctrl+O — ждать нечего,
					// лишние нажатия просто копятся до предела канала.
					select {
					case keys <- b:
					default:
					}
				case b == '\n':
					lines <- trimEOL(string(part))
					part = part[:0]
				default:
					part = append(part, b)
				}
			}
			if err != nil {
				// Хвост без перевода строки — тоже строка: без него
				// запрос, введённый без финального Enter, терялся.
				if len(part) > 0 {
					lines <- trimEOL(string(part))
				}
				return
			}
			if err == io.EOF {
				return
			}
		}
	}()

	return &stdinReader{lines: lines, keys: keys}
}

// trimEOL — убрать \r в конце строки (терминал Windows присылает CRLF)
// и хвостовые пробелы-табуляции.
func trimEOL(s string) string { return strings.TrimRight(s, "\r\n") }
