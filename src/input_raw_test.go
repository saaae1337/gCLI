package main

import (
	"testing"
	"time"
)

// Тесты разбора байтов ввода.
//
// Разбор — чистая функция feed(): горутина чтения os.Stdin в тестах не
// нужна, байты подаются прямо. Ридер в raw-режиме отдаёт события ввода
// (руны, клавиши, paste), композер решает, что с ними делать.

// rawReader — ридер в raw-режиме без реального терминала.
func rawReader() *stdinReader {
	s := &stdinReader{events: make(chan termEvent, 32)}
	s.raw.Store(true)
	return s
}

// nextEvent — достать одно событие с таймаутом (защита от повисания).
func nextEvent(t *testing.T, s *stdinReader) termEvent {
	t.Helper()
	select {
	case ev, ok := <-s.events:
		if !ok {
			t.Fatal("канал событий закрыт")
		}
		return ev
	case <-time.After(time.Second):
		t.Fatal("событие не пришло")
		return termEvent{}
	}
}

// feedSeq — скормить ридеру строку байт за раз.
func feedSeq(s *stdinReader, seq string) {
	for i := 0; i < len(seq); i++ {
		s.feed(seq[i])
	}
}

// expectKeys - собрать из потока событий клавиши по порядку, пропуская
// руны, и сверить с ожиданием.
func expectKeys(t *testing.T, s *stdinReader, want ...keyID) {
	t.Helper()
	var got []keyID
	deadline := time.After(2 * time.Second)
	for len(got) < len(want) {
		select {
		case ev := <-s.events:
			if ev.kind == evKey {
				got = append(got, ev.key)
			}
		case <-deadline:
			t.Fatalf("клавиши не дошли: ждали %v, собрали %v", want, got)
		}
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ождали %v, собрали %v", want, got)
		}
	}
}

// Enter в raw-режиме приходит как \r (ICRNL выключен) и отдаётся клавишей
// keyEnter; Ctrl+J (\n) - перевод строки внутри поля.
func TestRawEnterAndCtrlJ(t *testing.T) {
	s := rawReader()
	feedSeq(s, "прив")
	s.feed('\r')
	// Руны текста идут перед Enter.
	expectKeys(t, s, keyEnter)
	s2 := rawReader()
	s2.feed('\n')
	ev := nextEvent(t, s2)
	if ev.key != keyShiftEnter {
		t.Errorf("\\n отдан не как перевод строки: %+v", ev)
	}
}

// Backspace и Ctrl-комбинации отдаются клавишами, не попадая в текст.
func TestRawCtrlKeys(t *testing.T) {
	s := rawReader()
	feedSeq(s, "ки\x7f\x15\x03\x04\x0f\x0b\x1b[3~")
	// Backspace и Ctrl-комбинации дошли клавишами, в текст не попали.
	expectKeys(t, s, keyBackspace, keyCtrlU, keyCtrlC, keyCtrlD, keyCtrlO, keyCtrlK, keyDelete)
}

// Стрелки (CSI) и прочие escape-последовательности превращаются в клавиши,
// а в текст не попадают: раньше стрелка вставляла в запрос мусор вида «[A».
func TestEscapeSequencesToKeys(t *testing.T) {
	cases := []struct {
		seq string
		key keyID
	}{
		{"\x1b[A", keyUp},
		{"\x1b[B", keyDown},
		{"\x1b[C", keyRight},
		{"\x1b[D", keyLeft},
		{"\x1b[H", keyHome},
		{"\x1b[F", keyEnd},
		{"\x1b[3~", keyDelete},
		{"\x1b[1;5C", keyRight},
		{"\x1bOD", keyLeft},
		{"\x1bOB", keyDown},
		{"\x1b[Z", keyBackTab},
	}
	for _, c := range cases {
		s := rawReader()
		s.feed('a')
		feedSeq(s, c.seq)
		s.feed('b')
		s.feed('\r')
		ev := nextEvent(t, s)
		if ev.kind != evRune || ev.r != 'a' {
			t.Fatalf("%s: первая руна потеряна: %+v", c.seq, ev)
		}
		ev = nextEvent(t, s)
		if ev.kind != evKey || ev.key != c.key {
			t.Fatalf("%s: ождали %v, получили %+v", c.seq, c.key, ev)
		}
		ev = nextEvent(t, s)
		if ev.kind != evRune || ev.r != 'b' {
			t.Fatalf("%s: руна после последовательности потеряна: %+v", c.seq, ev)
		}
	}
}

// Escape-последовательность, разрезанная границей чтения (ESC в одном
// пакете, хвост в другом), всё равно разбирается целиком: состояние живёт
// между вызовами feed.
func TestEscapeSequenceAcrossFeeds(t *testing.T) {
	s := rawReader()
	s.feed(0x1B)
	s.feed('[')
	s.feed('B')
	s.feed('z')
	ev := nextEvent(t, s)
	if ev.key != keyDown {
		t.Fatalf("ождали keyDown, получили %+v", ev)
	}
	ev = nextEvent(t, s)
	if ev.r != 'z' {
		t.Fatalf("руна после последовательности потеряна: %+v", ev)
	}
}

// Одиночный ESC - клавиша keyEsc: таймер решает, что продолжения не будет.
func TestLoneEscBecomesKey(t *testing.T) {
	s := rawReader()
	s.feed(0x1B)
	ev := nextEvent(t, s)
	if ev.kind != evKey || ev.key != keyEsc {
		t.Fatalf("ождали keyEsc, получили %+v", ev)
	}
	// ESC ESC подряд (быстрее таймера): оба переводятся в один armEsc -
	// отдаётся одна клавиша, залипания таймеров не происходит.
	s.feed(0x1B)
	s.feed(0x1B)
	ev = nextEvent(t, s)
	if ev.key != keyEsc {
		t.Fatalf("двойной ESC: ождали keyEsc, получили %+v", ev)
	}
}

// Bracketed paste: текст между маркерами приходит одним событием.
func TestBracketedPaste(t *testing.T) {
	s := rawReader()
	feedSeq(s, "\x1b[200~при\tвет\nмир\x1b[201~")
	ev := nextEvent(t, s)
	if ev.kind != evPaste {
		t.Fatalf("ождали evPaste, получили %+v", ev)
	}
	if got := string(ev.runes); got != "при\tвет\nмир" {
		t.Errorf("paste = %q", got)
	}
}

// В canonical-режиме (пайп, CI) поведение прежнее: строка по \n, Ctrl+O
// клавишей, escape-последовательности глотаются.
func TestCookedModeUnchanged(t *testing.T) {
	s := &stdinReader{events: make(chan termEvent, 8)}
	feedSeq(s, "аб")
	s.feed('\n')
	ev := nextEvent(t, s)
	if ev.kind != evLine || ev.line != "аб" {
		t.Errorf("строка = %+v, ждали evLine \"аб\"", ev)
	}
	s.feed(0x0F)
	ev = nextEvent(t, s)
	if ev.kind != evKey || ev.key != keyCtrlO {
		t.Fatalf("Ctrl+O потерян в canonical-режиме: %+v", ev)
	}
	// Escape-последовательности отдаются клавишами и в cooked-режиме,
	// но в строку не попадают.
	s.feed('x')
	feedSeq(s, "\x1b[A")
	s.feed('y')
	s.feed('\n')
	var line string
	sawUp := false
	for line == "" {
		ev := nextEvent(t, s)
		switch ev.kind {
		case evKey:
			if ev.key == keyUp {
				sawUp = true
			}
		case evLine:
			line = ev.line
		}
	}
	if line != "xy" {
		t.Errorf("строка = %q, ждали \"xy\"", line)
	}
	if !sawUp {
		t.Error("стрелка в cooked-режиме не отдалась клавишей")
	}
}

// Многобайтовая руна собирается из отдельных байтов.
func TestMultibyteRuneAssembly(t *testing.T) {
	s := rawReader()
	for _, b := range []byte("щ") {
		s.feed(b)
	}
	ev := nextEvent(t, s)
	if ev.kind != evRune || ev.r != 'щ' {
		t.Fatalf("руна = %+v, ждали 'щ'", ev)
	}
}

// enableRaw/disableRaw идемпотентны: повторное включение не перезатирает
// restore-функцию, повторное выключение не паникует.
func TestEnableDisableRawIdempotent(t *testing.T) {
	s := &stdinReader{events: make(chan termEvent, 8)}
	s.disableRaw() // выключение до включения — ничего
	s.enableRaw()  // не терминал → raw не включится, но и не сломается
	s.enableRaw()
	s.disableRaw()
	s.disableRaw()
	if s.raw.Load() {
		t.Fatal("raw должен быть выключен")
	}
}
