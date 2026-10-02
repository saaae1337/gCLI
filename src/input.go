package main

import (
	"io"
	"os"
	"strings"
	"sync/atomic"
)

// ---------- Ввод: один владелец stdin ----------
//
// Читать stdin из двух мест нельзя: буфер bufio.Scanner и отдельный
// обработчик Ctrl+O разъедали бы друг другу байты (в paste-буфере это
// выглядело бы как «пропала первая буква запроса»). Поэтому stdin читает
// ровно одна горутина — stdinReader, — а все остальные берут готовое из
// каналов.
//
// Канал lines отдаёт готовые строки (без перевода строки), keys —
// горячие клавиши. Escape-последовательности (стрелки и прочее) глотаются:
// раньше они попадали в строку ввода мусором.
//
// В REPL терминал переводится в точечный raw-режим (см. term_linux.go):
// canonical-режим перехватывал Ctrl+O (VDISCARD при IEXTEN) и байт до
// приложения не доходил вовсе. В raw-режиме байты приходят сразу, но эхо
// и стирание строки становятся нашей работой — их делает feedRaw. Вне
// терминала (пайпы, CI) raw не включается, и ввод идёт построчно как
// раньше: Enter отдаёт строку, ничего лишнего не приходит.

const ctrlO = 0x0F // ^O

// stdinReader — построчный ввод для REPL и диалогов.
type stdinReader struct {
	lines chan string
	keys  chan byte
	cur   string
	ok    bool

	// raw — включён ли точечный raw-режим терминала. Читается на каждый
	// байт, поэтому atomic: включение случается после старта горутины.
	raw     atomic.Bool
	restore func() // возврат терминала в исходный режим; nil, если raw не включался

	// out — куда печатать эхо в raw-режиме. nil = os.Stdout; в тестах — буфер.
	out io.Writer

	// part — текущая недовведённая строка, esc — состояние глотателя
	// escape-последовательностей (0 — обычный байт, 1 — после ESC,
	// 2 — внутри CSI, 3 — после ESC O).
	part []byte
	esc  int
}

// Scan — прочитать следующую строку (как у bufio.Scanner).
func (s *stdinReader) Scan() bool {
	s.cur, s.ok = <-s.lines
	return s.ok
}

// Text — последняя прочитанная строка.
func (s *stdinReader) Text() string { return s.cur }

// Keys — канал горячих клавиш. Читает его ровно один на сеанс
// watchKeys (см. main): и во время хода, и между ходами. Вне хода
// нажатия копятся в буфере, чтобы Ctrl+C и прочее не потерялись.
func (s *stdinReader) Keys() <-chan byte { return s.keys }

// enableRaw — включить raw-режим терминала (если это терминал).
//
// Идемпотентна: повторный вызов ничего не меняет. Если stdin — не терминал
// (пайп, файл), raw молча не включается: построчный ввод работает как
// раньше, горячие клавиши просто ждут Enter, как и до raw-режима.
func (s *stdinReader) enableRaw() {
	if s == nil || s.raw.Swap(true) {
		return
	}
	restore, ok := makeRawStdin()
	if !ok {
		s.raw.Store(false)
		return
	}
	s.restore = restore
}

// disableRaw — вернуть терминал в исходный режим. Идемпотентна.
//
// Обязательна на каждом пути выхода из REPL, включая Ctrl+C: процесс,
// умерший с выключенным ECHO, оставляет пользователю «слепой» терминал,
// где ввод не виден. Поэтому её зовут и defer в main, и обработчик сигнала
// перед os.Exit.
func (s *stdinReader) disableRaw() {
	if s == nil || !s.raw.Swap(false) {
		return
	}
	if s.restore != nil {
		s.restore()
		s.restore = nil
	}
}

// newStdin — запустить фоновый ридер и вернуть обёртку.
//
// Единственный отказ — прочитать нечего (закрытый или перенаправленный
// ввод). В этом случае REPL сразу увидит EOF и попрощается.
func newStdin() *stdinReader {
	lines := make(chan string, 8)
	keys := make(chan byte, 8)
	s := &stdinReader{lines: lines, keys: keys}

	go func() {
		defer close(lines)
		defer close(keys)
		in := os.Stdin
		buf := make([]byte, 4096)
		for {
			n, err := in.Read(buf)
			for _, b := range buf[:n] {
				if s.feed(b) {
					// Ctrl+D на пустой строке в raw-режиме: честный EOF.
					return
				}
			}
			if err != nil {
				// Хвост без перевода строки — тоже строка: без него
				// запрос, введённый без финального Enter, терялся.
				if len(s.part) > 0 {
					s.lines <- trimEOL(string(s.part))
					s.part = s.part[:0]
				}
				return
			}
		}
	}()

	return s
}

// feed — разобрать один байт ввода. Возвращает true, если ввод кончился
// (Ctrl+D на пустой строке в raw-режиме).
func (s *stdinReader) feed(b byte) bool {
	if s.raw.Load() {
		return s.feedRaw(b)
	}
	return s.feedCooked(b)
}

// feedCooked — разбор байта в canonical-режиме (пайп, CI, старое поведение).
// Терминал сам делает эхо и стирание, до нас доходят готовые строки;
// наша работа — вылавливать Ctrl+O и глотать escape-последовательности,
// которые здесь копились бы в строке мусором.
func (s *stdinReader) feedCooked(b byte) bool {
	switch {
	case b == ctrlO:
		s.sendKey(b)
	case b == '\n':
		s.emitLine()
	default:
		if !s.swallowEscape(b) {
			s.part = append(s.part, b)
		}
	}
	return false
}

// feedRaw — разбор байта в raw-режиме.
//
// Здесь терминал ничего не делает за нас: эхо, стирание и перевод строки —
// наши. Правила:
//
//	\r или \n   — конец строки (ICRNL на Linux сводит оба к '\n', но
//	              Windows присылает \r, поэтому обрабатываем оба);
//	0x7F/0x08   — Backspace, стирает последний рунический символ;
//	Ctrl+O      — горячая клавиша, в строку не попадает;
//	Ctrl+D      — EOF, но только на пустой строке (как в shell);
//	Ctrl+U      — стереть всё набранное;
//	прочее <0x20 — игнорировать (ISIG-байты до нас не доходят, а случайные
//	              управляющие байты ломали бы строку);
//	остальное   — в строку и эхом на экран.
func (s *stdinReader) feedRaw(b byte) bool {
	switch {
	case b == '\n' || b == '\r':
		s.echoWrite([]byte("\n"))
		s.emitLine()
	case b == 0x7F || b == 0x08:
		s.eraseLastRune()
	case b == ctrlO:
		s.sendKey(b)
	case b == 0x04:
		if len(s.part) == 0 {
			return true
		}
	case s.swallowEscape(b):
		// байт escape-последовательности (стрелки, Home, …) — в строку
		// не попадает и на экран не печатается. Важно: проверка стоит
		// РАНЬШЕ ветки «управляющие байты», потому что сам ESC (0x1B) —
		// тоже управляющий байт, и ветка не пропустила бы его глотателю.
	case b < 0x20 && b != '\t':
		if b == 0x15 { // Ctrl+U
			for len(s.part) > 0 {
				s.eraseLastRune()
			}
		}
	default:
		s.part = append(s.part, b)
		s.echoWrite([]byte{b})
	}
	return false
}

// emitLine — строка введена: отдать в канал и начать новую.
func (s *stdinReader) emitLine() {
	s.esc = 0
	s.lines <- trimEOL(string(s.part))
	s.part = s.part[:0]
}

// sendKey — положить горячую клавишу в канал. Канал буферизован и не
// блокируется: залипающий Ctrl+O копит нажатия до предела канала, лишние
// теряются — это лучше, чем остановка чтения stdin.
func (s *stdinReader) sendKey(b byte) {
	select {
	case s.keys <- b:
	default:
	}
}

// swallowEscape — распознать байт escape-последовательности. Возвращает
// true, если байт часть последовательности и в строку ему делать нечего.
//
// Поддержаны два формы: CSI (ESC [ параметры финал — стрелки, Home, End)
// и SS3 (ESC O буква — те же клавиши на некоторых терминалах). Одиночный
// ESC (Alt-комбинации) глотается парой: ESC + следующий байт.
func (s *stdinReader) swallowEscape(b byte) bool {
	switch s.esc {
	case 1: // предыдущий байт — ESC
		switch b {
		case '[':
			s.esc = 2
		case 'O':
			s.esc = 3
		default:
			s.esc = 0
		}
		return true
	case 2: // внутри CSI: параметры 0x30–0x3F, промежуточные 0x20–0x2F,
		// финальный байт 0x40–0x7E закрывает последовательность
		if b >= 0x40 && b <= 0x7E {
			s.esc = 0
		}
		return true
	case 3: // SS3: ESC O + ровно один байт
		s.esc = 0
		return true
	}
	if b == 0x1B {
		s.esc = 1
		return true
	}
	return false
}

// eraseLastRune — Backspace в raw-режиме: срезать с part последний рунический
// символ (не байт: срез по границе UTF-8) и затереть его на экране.
//
// Затирание «\b \b» сдвигает курсор на одну колонку; для широких
// символов (CJK) останется артефакт — примем как плату за raw без
// полноценного line-editing: важнее, что Backspace работает вообще.
func (s *stdinReader) eraseLastRune() {
	n := len(s.part)
	if n == 0 {
		return
	}
	for n > 0 && s.part[n-1]&0xC0 == 0x80 {
		n-- // байты-продолжения UTF-8
	}
	if n > 0 {
		n--
	}
	s.part = s.part[:n]
	s.echoWrite([]byte("\b \b"))
}

// echoWrite — печать эха в raw-режиме. Идёт мимо UI-мьютекса намеренно:
// терминал в canonical-режиме печатал эхо так же напрямую, и гонка здесь
// та же, что была всегда, — зато ввод не подвисает на печати хода.
func (s *stdinReader) echoWrite(p []byte) {
	w := s.out
	if w == nil {
		w = os.Stdout
	}
	_, _ = w.Write(p)
}

// trimEOL — убрать \r в конце строки (терминал Windows присылает CRLF)
// и хвостовые пробелы-табуляции.
func trimEOL(s string) string { return strings.TrimRight(s, "\r\n") }
