package main

import (
	"bytes"
	"testing"
)

// Тесты разбора байтов ввода.
//
// Разбор — чистая функция feed(): горутина чтения os.Stdin в тестах не
// нужна, байты подаются прямо. Так проверяется то, что раньше было
// недостижимо для тестов — а именно оно и сломалось: Ctrl+O в
// canonical-режиме съедал line discipline терминала, до feed() байт не
// доходил, и канальные тесты этого не видели.

// rawReader — ридер в raw-режиме с эхом в буфер.
func rawReader() (*stdinReader, *bytes.Buffer) {
	s := &stdinReader{
		lines: make(chan string, 8),
		keys:  make(chan byte, 8),
		out:   &bytes.Buffer{},
	}
	s.raw.Store(true)
	return s, s.out.(*bytes.Buffer)
}

// line — достать одну строку из канала с таймаутом (защита от повисания).
func line(t *testing.T, s *stdinReader) string {
	t.Helper()
	select {
	case l, ok := <-s.lines:
		if !ok {
			t.Fatal("канал строк закрыт")
		}
		return l
	default:
		t.Fatal("строка не пришла")
		return ""
	}
}

// Enter в raw-режиме приходит как \r (Windows) или \n (Linux с ICRNL) —
// оба обязаны заканчивать строку.
func TestRawEnterCRandLF(t *testing.T) {
	for _, eol := range []byte{'\r', '\n'} {
		s, _ := rawReader()
		for _, b := range []byte("прив") {
			s.feed(b)
		}
		s.feed(eol)
		if got := line(t, s); got != "прив" {
			t.Errorf("eol=%q: строка = %q, ждали «прив»", eol, got)
		}
	}
}

// Backspace стирает последний рунический символ, а не байт: срез по
// границе UTF-8. «ки» — два символа по два байта: один 0x7F срезает «и»
// целиком, три подряд не паникуют на пустой строке.
func TestRawBackspaceErasesRune(t *testing.T) {
	s, echo := rawReader()
	for _, b := range []byte("ки") {
		s.feed(b)
	}
	s.feed(0x7F)
	s.feed('\r')
	// Если бы Backspace резал по байтам, здесь остался бы обкусанный
	// «к» плюс байт-продолжение «и» — рунический срез оставляет ровно «к».
	if got := line(t, s); got != "к" {
		t.Errorf("после одного Backspace = %q, ждали «к»", got)
	}
	if !bytes.Contains(echo.Bytes(), []byte("\b \b")) {
		t.Errorf("эхо затирания не напечатано: %q", echo.String())
	}
	// Три Backspace на пустой строке — не паника и не мусор.
	s.feed(0x7F)
	s.feed(0x7F)
	s.feed(0x7F)
	s.feed('!')
	s.feed('\r')
	if got := line(t, s); got != "!" {
		t.Errorf("после лишних Backspace строка = %q, ждали «!»", got)
	}
}

// Ctrl+O в raw-режиме доходит до канала клавиш сразу, не дожидаясь Enter.
// Это и есть фикс главного бага: раньше байт перехватывал терминал.
func TestRawCtrlOReachesKeysImmediately(t *testing.T) {
	s, _ := rawReader()
	s.feed(ctrlO)
	select {
	case b := <-s.keys:
		if b != ctrlO {
			t.Fatalf("ключ = %#x, ждали %#x", b, ctrlO)
		}
	default:
		t.Fatal("Ctrl+O не дошёл до канала клавиш")
	}
	// и в строку он не попал
	s.feed('x')
	s.feed('\r')
	if got := line(t, s); got != "x" {
		t.Errorf("Ctrl+O протёк в строку: %q", got)
	}
}

// Ctrl+D на пустой строке — EOF (feed возвращает true), с текстом — ничего.
func TestRawCtrlDEOFOnlyOnEmptyLine(t *testing.T) {
	s, _ := rawReader()
	s.feed('a')
	if s.feed(0x04) {
		t.Fatal("Ctrl+D с текстом в строке не должен закрывать ввод")
	}
	s.feed(0x7F)
	if !s.feed(0x04) {
		t.Fatal("Ctrl+D на пустой строке должен быть EOF")
	}
}

// Ctrl+U стирает всё набранное.
func TestRawCtrlUClearsLine(t *testing.T) {
	s, _ := rawReader()
	for _, b := range []byte("хвост") {
		s.feed(b)
	}
	s.feed(0x15)
	s.feed('\r')
	if got := line(t, s); got != "" {
		t.Errorf("после Ctrl+U строка = %q, ждали пустую", got)
	}
}

// Стрелки (CSI) и прочие escape-последовательности в строку не попадают:
// раньше стрелка вставляла в запрос мусор вида «[A».
func TestEscapeSequencesSwallowed(t *testing.T) {
	seqs := []string{
		"\x1b[A",    // Up
		"\x1b[3~",   // Delete
		"\x1b[1;5C", // Ctrl+Right
		"\x1bOD",    // SS3 Left
	}
	for _, seq := range seqs {
		s, _ := rawReader()
		s.feed('a')
		for i := 0; i < len(seq); i++ {
			s.feed(seq[i])
		}
		s.feed('b')
		s.feed('\r')
		if got := line(t, s); got != "ab" {
			t.Errorf("последовательность %q испортила строку: %q", seq, got)
		}
	}
}

// Escape-последовательность, разрезанная границей чтения (ESC в одном
// пакете, хвост в другом), всё равно глотается целиком: состояние живёт
// между вызовами feed.
func TestEscapeSequenceAcrossFeeds(t *testing.T) {
	s, _ := rawReader()
	s.feed(0x1B)
	s.feed('[')
	s.feed('B')
	s.feed('z')
	s.feed('\r')
	if got := line(t, s); got != "z" {
		t.Errorf("строка = %q, ждали «z»", got)
	}
}

// Прочие управляющие байты (<0x20) в raw-режиме не ломают строку.
func TestRawIgnoresRandomControlBytes(t *testing.T) {
	s, _ := rawReader()
	s.feed('a')
	s.feed(0x02) // Ctrl+B
	s.feed(0x0C) // Ctrl+L
	s.feed('b')
	s.feed('\r')
	if got := line(t, s); got != "ab" {
		t.Errorf("строка = %q, ждали «ab»", got)
	}
}

// В canonical-режиме (пайп, CI) поведение прежнее: строка по \n, Ctrl+O
// в ключи, обычные байты в строку.
func TestCookedModeUnchanged(t *testing.T) {
	s := &stdinReader{lines: make(chan string, 8), keys: make(chan byte, 8)}
	for _, b := range []byte("аб") {
		s.feed(b)
	}
	s.feed('\n')
	if got := line(t, s); got != "аб" {
		t.Errorf("строка = %q, ждали «аб»", got)
	}
	s.feed(ctrlO)
	select {
	case b := <-s.keys:
		if b != ctrlO {
			t.Fatalf("ключ = %#x", b)
		}
	default:
		t.Fatal("Ctrl+O потерян в canonical-режиме")
	}
	// и escape-глотатель здесь тоже работает
	s.feed('x')
	s.feed(0x1B)
	s.feed('[')
	s.feed('A')
	s.feed('y')
	s.feed('\n')
	if got := line(t, s); got != "xy" {
		t.Errorf("строка = %q, ждали «xy»", got)
	}
}

// enableRaw/disableRaw идемпотентны: повторное включение не перезатирает
// restore-функцию, повторное выключение не паникует.
func TestEnableDisableRawIdempotent(t *testing.T) {
	s := &stdinReader{lines: make(chan string, 8), keys: make(chan byte, 8)}
	s.disableRaw() // выключение до включения — ничего
	s.enableRaw()  // не терминал → raw не включится, но и не сломается
	s.enableRaw()
	s.disableRaw()
	s.disableRaw()
	if s.raw.Load() {
		t.Fatal("raw должен быть выключен")
	}
}
