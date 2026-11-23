package main

import (
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// ---------- Ввод: один владелец stdin ----------
//
// Читать stdin из двух мест нельзя: разъедались бы байты. Поэтому stdin
// читает ровно одна горутина — stdinReader, — а все остальные получают
// готовые события из канала.
//
// Событийная модель (интерфейс Claude Code): композер получает не строки,
// а клавиши — руны, стрелки, Ctrl-комбинации, bracketed paste и одиночный
// Esc. Одиночный Esc отличим от начала escape-последовательности таймером:
// если за 30 мс после ESC байтов не пришло — это клавиша Esc, если пришли —
// разбираем CSI/SS3.
//
// В REPL терминал переводится в полный raw-режим (см. term_tui_*.go):
// ISIG выключен, Ctrl+C приходит байтом 0x03 и обрабатывается композером
// (первое нажатие прерывает ход, второе выходит). Вне терминала (пайпы, CI)
// raw не включается: Enter отдаёт готовую строку через evLine.

// evKind - вид события ввода.
type evKind int

const (
	evRune  evKind = iota // печатный символ
	evKey                 // клавиша (стрелки, Ctrl-*, Esc, Tab...)
	evPaste               // bracketed paste: руны в runes
	evLine                // cooked-режим: готовая строка
	evEOF                 // ввод закрыт
)

// keyID - клавиша события evKey.
type keyID int

const (
	keyNone keyID = iota
	keyUp
	keyDown
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyDelete
	keyBackspace
	keyTab
	keyBackTab
	keyEsc
	keyEnter
	keyShiftEnter // Ctrl+J / Shift+Enter: перевод строки внутри поля
	keyCtrlA
	keyCtrlE
	keyCtrlC
	keyCtrlD
	keyCtrlK
	keyCtrlL
	keyCtrlO
	keyCtrlN
	keyCtrlP
	keyCtrlR
	keyCtrlT
	keyCtrlU
	keyCtrlW
	keyPageUp
	keyPageDown
)

// termEvent - одно событие ввода.
type termEvent struct {
	kind  evKind
	r     rune
	key   keyID
	runes []rune
	line  string
}

// escTimeout - окно ожидания продолжения escape-последовательности.
const escTimeout = 30 * time.Millisecond

// stdinReader - событийный ридер stdin.
type stdinReader struct {
	events chan termEvent

	// raw - включён ли полный raw-режим терминала. Atomic: включение
	// случается после старта горутины.
	raw atomic.Bool
	// rawMu - сериализует включение/выключение raw-режима и состояние
	// машины escape-последовательностей между ридером и таймером Esc.
	rawMu   sync.Mutex
	restore func() // возврат терминала в исходный режим; nil, если raw не включался

	// part - накопитель многобайтовой руны и строки cooked-режима.
	part []byte
	// esc - состояние глотателя escape-последовательностей
	// (0 - обычный байт, 1 - после ESC, 2 - внутри CSI, 3 - после ESC O).
	esc int
	// escPend - одиночный ESC ждёт решения (таймер или следующий байт).
	escPend  bool
	escTimer *time.Timer
	// paste - идёт bracketed paste: байты копятся до закрывающего маркера.
	pasteOn  bool
	pasteBuf []byte
	// csi - накопленные параметры CSI-последовательности.
	csi []byte

	// cur/ok - последняя строка для Scan/Text (пути до старта композера).
	cur string
	ok  bool
}

// Events - канал событий ввода. Единственный потребитель - композер REPL.
func (s *stdinReader) Events() <-chan termEvent { return s.events }

// Scan - прочитать следующую строку (как у bufio.Scanner).
//
// Путь для кода ДО старта композера: мастер настройки -setup, тесты.
// Внутри REPL строковый ввод ведёт композер, и второй читатель событий
// здесь не нужен.
func (s *stdinReader) Scan() bool {
	if s == nil {
		s.cur, s.ok = "", false
		return false
	}
	for {
		ev, ok := <-s.events
		if !ok {
			s.cur, s.ok = "", false
			return false
		}
		switch ev.kind {
		case evLine:
			s.cur, s.ok = ev.line, true
			return true
		case evEOF:
			s.cur, s.ok = "", false
			return false
		}
	}
}

// Text - последняя прочитанная строка (после Scan).
func (s *stdinReader) Text() string { return s.cur }

// enableRaw - включить полный raw-режим терминала (если это терминал).
//
// Идемпотентна. Если stdin - не терминал (пайп, файл), raw молча не
// включается: ввод идёт построчно через evLine.
func (s *stdinReader) enableRaw() {
	if s == nil {
		return
	}
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	if s.raw.Swap(true) {
		return
	}
	restore, ok := makeRawTUI()
	if !ok {
		s.raw.Store(false)
		return
	}
	s.restore = restore
}

// disableRaw - вернуть терминал в исходный режим. Идемпотентна.
//
// Обязательна на каждом пути выхода из REPL, включая Ctrl+C: процесс,
// умерший с выключенным ECHO, оставляет пользователю «слепой» терминал.
func (s *stdinReader) disableRaw() {
	if s == nil {
		return
	}
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	if !s.raw.Swap(false) {
		return
	}
	s.stopEscTimerLocked()
	if s.restore != nil {
		s.restore()
		s.restore = nil
	}
}

// newStdin - запустить фоновый ридер и вернуть обёртку.
//
// Единственный отказ - прочитать нечего (закрытый или перенаправленный
// ввод). В этом случае композер сразу увидит evEOF и REPL попрощается.
func newStdin() *stdinReader {
	s := &stdinReader{events: make(chan termEvent, 64)}

	go func() {
		defer close(s.events)
		in := os.Stdin
		buf := make([]byte, 4096)
		for {
			n, err := in.Read(buf)
			for _, b := range buf[:n] {
				if s.feed(b) {
					return
				}
			}
			if err != nil {
				// Хвост без перевода строки - тоже строка: без него
				// запрос, введённый без финального Enter, терялся.
				s.flushPart()
				return
			}
		}
	}()

	return s
}

// flushPart - отдасть накопленную строку (конец ввода, EOF).
func (s *stdinReader) flushPart() {
	if len(s.part) > 0 {
		s.emit(termEvent{kind: evLine, line: trimEOL(string(s.part))})
		s.part = s.part[:0]
	}
}

// emit - положить событие в канал. Канал буферизован; при переполнении
// событие теряется - лучше потерять нажатие, чем остановить чтение stdin.
func (s *stdinReader) emit(ev termEvent) {
	select {
	case s.events <- ev:
	default:
	}
}

// feed - разобрать один байт ввода. Возвращает true, если ввод кончился.
func (s *stdinReader) feed(b byte) bool {
	if s.raw.Load() {
		return s.feedRaw(b)
	}
	return s.feedCooked(b)
}

// feedCooked - разбор байта в canonical-режиме (пайп, CI).
// Терминал сам делает эхо и стирание, до нас доходят готовые строки;
// наша работа - вылавливать Ctrl+O и глотать escape-последовательности.
func (s *stdinReader) feedCooked(b byte) bool {
	switch {
	case b == 0x0F: // Ctrl+O
		s.emit(termEvent{kind: evKey, key: keyCtrlO})
	case b == '\n':
		s.flushPart()
	default:
		if !s.swallowEscape(b) {
			s.part = append(s.part, b)
		}
	}
	return false
}

// feedRaw - разбор байта в raw-режиме.
func (s *stdinReader) feedRaw(b byte) bool {
	// Внутри bracketed paste всё копится до закрывающего маркера.
	if s.pasteOn {
		if s.swallowEscape(b) {
			return false
		}
		s.pasteBuf = append(s.pasteBuf, b)
		return false
	}

	switch {
	case b == '\r': // Enter (ICRNL выключен: Enter приходит CR)
		s.emit(termEvent{kind: evKey, key: keyEnter})
	case b == '\n': // Ctrl+J: перевод строки внутри поля
		s.emit(termEvent{kind: evKey, key: keyShiftEnter})
	case b == '\t':
		s.emit(termEvent{kind: evKey, key: keyTab})
	case b == 0x7F || b == 0x08:
		s.emit(termEvent{kind: evKey, key: keyBackspace})
	case b == 0x1B:
		s.armEsc()
	case b < 0x20:
		s.feedCtrl(b)
	case s.swallowEscape(b):
		// байт escape-последовательности - сам по себе в события не попадает
	default:
		s.feedRuneByte(b)
	}
	return false
}

// feedCtrl - управляющий байт (Ctrl-комбинация) в raw-режиме.
func (s *stdinReader) feedCtrl(b byte) {
	var k keyID
	switch b {
	case 0x01:
		k = keyCtrlA
	case 0x03:
		k = keyCtrlC
	case 0x04:
		k = keyCtrlD
	case 0x05:
		k = keyCtrlE
	case 0x0B:
		k = keyCtrlK
	case 0x0C:
		k = keyCtrlL
	case 0x0E:
		k = keyCtrlN
	case 0x0F:
		k = keyCtrlO
	case 0x10:
		k = keyCtrlP
	case 0x12:
		k = keyCtrlR
	case 0x14:
		k = keyCtrlT
	case 0x15:
		k = keyCtrlU
	case 0x17:
		k = keyCtrlW
	default:
		return // случайные управляющие байты ломали бы ввод
	}
	s.emit(termEvent{kind: evKey, key: k})
}

// feedRuneByte - печатный байт: собрать многобайтовую руну и отдать.
func (s *stdinReader) feedRuneByte(b byte) {
	s.part = append(s.part, b)
	if b < 0x80 || utf8.FullRune(s.part) {
		text := string(s.part)
		s.part = s.part[:0]
		for _, r := range text {
			s.emit(termEvent{kind: evRune, r: r})
		}
	}
}

// armEsc - одиночный ESC: завести таймер. Если за escTimeout не пришло
// продолжение - это клавиша Esc (прерывание хода, закрытие меню).
func (s *stdinReader) armEsc() {
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	if s.escTimer != nil {
		s.escTimer.Stop()
	}
	s.escPend = true
	s.escTimer = time.AfterFunc(escTimeout, func() {
		s.rawMu.Lock()
		if !s.escPend {
			s.rawMu.Unlock()
			return
		}
		s.escPend = false
		s.escTimer = nil
		s.rawMu.Unlock()
		s.emit(termEvent{kind: evKey, key: keyEsc})
	})
}

// stopEscTimerLocked - снять таймер одиночного ESC (под rawMu).
func (s *stdinReader) stopEscTimerLocked() {
	s.escPend = false
	if s.escTimer != nil {
		s.escTimer.Stop()
		s.escTimer = nil
	}
}

// swallowEscape - распознать байт escape-последовательности. Возвращает
// true, если байт часть последовательности.
//
// Поддержаны CSI (ESC [ параметры финал - стрелки, Home, Delete, paste)
// и SS3 (ESC O буква - те же клавиши на некоторых терминалах). Одиночный
// ESC решается таймером (armEsc); ESC [ и ESC O отменяют его и открывают
// последовательность.
func (s *stdinReader) swallowEscape(b byte) bool {
	s.rawMu.Lock()
	if s.escPend {
		// ESC, ожидавший решения, получил продолжение: таймер снят,
		// дальше разбираем последовательность.
		s.stopEscTimerLocked()
		if b == 0x1B {
			// ESC ESC: первый ESC так и остался одиночкой.
			s.esc = 0
			s.rawMu.Unlock()
			s.emit(termEvent{kind: evKey, key: keyEsc})
			s.armEsc()
			return true
		}
		s.esc = 1
		s.rawMu.Unlock()
		return s.swallowEscape(b)
	}
	s.rawMu.Unlock()

	switch s.esc {
	case 0:
		if b == 0x1B {
			// ESC без pend (внутри paste или cooked-режим): включаем
			// машину состояний, чтобы хвост последовательности не попал
			// в контент.
			s.esc = 1
			return true
		}
		return false
	case 1: // предыдущий байт - ESC
		switch b {
		case '[':
			s.esc = 2
			s.csi = s.csi[:0]
		case 'O':
			s.esc = 3
		case 0x1B:
			s.esc = 0
			s.emit(termEvent{kind: evKey, key: keyEsc})
			s.armEsc()
		default:
			// Alt-комбинация: глотаем пару целиком.
			s.esc = 0
		}
		return true
	case 2: // внутри CSI: параметры 0x30-0x3F, промежуточные 0x20-0x2F,
		// финальный байт 0x40-0x7E закрывает последовательность
		if b >= 0x30 && b <= 0x3F {
			s.csi = append(s.csi, b)
			return true
		}
		if b >= 0x40 && b <= 0x7E {
			s.esc = 0
			s.finishCSI(b)
		}
		return true
	case 3: // SS3: ESC O + ровно один байт
		s.esc = 0
		switch b {
		case 'A':
			s.emit(termEvent{kind: evKey, key: keyUp})
		case 'B':
			s.emit(termEvent{kind: evKey, key: keyDown})
		case 'C':
			s.emit(termEvent{kind: evKey, key: keyRight})
		case 'D':
			s.emit(termEvent{kind: evKey, key: keyLeft})
		case 'H':
			s.emit(termEvent{kind: evKey, key: keyHome})
		case 'F':
			s.emit(termEvent{kind: evKey, key: keyEnd})
		}
		return true
	}
	s.esc = 0
	return b == 0x1B
}

// finishCSI - CSI-последовательность закрылась финальным байтом.
func (s *stdinReader) finishCSI(final byte) {
	params := string(s.csi)
	switch final {
	case 'A':
		s.emit(termEvent{kind: evKey, key: keyUp})
	case 'B':
		s.emit(termEvent{kind: evKey, key: keyDown})
	case 'C':
		s.emit(termEvent{kind: evKey, key: keyRight})
	case 'D':
		s.emit(termEvent{kind: evKey, key: keyLeft})
	case 'H':
		s.emit(termEvent{kind: evKey, key: keyHome})
	case 'F':
		s.emit(termEvent{kind: evKey, key: keyEnd})
	case 'Z':
		s.emit(termEvent{kind: evKey, key: keyBackTab})
	case 'M', 'm':
		// координаты мыши - игнорируем, клавиатура важнее
	case '~':
		switch params {
		case "1", "7":
			s.emit(termEvent{kind: evKey, key: keyHome})
		case "3":
			s.emit(termEvent{kind: evKey, key: keyDelete})
		case "4", "8":
			s.emit(termEvent{kind: evKey, key: keyEnd})
		case "5":
			s.emit(termEvent{kind: evKey, key: keyPageUp})
		case "6":
			s.emit(termEvent{kind: evKey, key: keyPageDown})
		case "200":
			// начало bracketed paste
			s.pasteOn = true
			s.pasteBuf = s.pasteBuf[:0]
		case "201":
			// конец bracketed paste: отдать накопленное рунами
			s.pasteOn = false
			if len(s.pasteBuf) > 0 {
				text := string(s.pasteBuf)
				s.pasteBuf = s.pasteBuf[:0]
				if !utf8.ValidString(text) {
					text = string([]rune(text))
				}
				s.emit(termEvent{kind: evPaste, runes: []rune(text)})
			}
		}
	}
}

// trimEOL - убрать \r\n в конце строки (терминал Windows присылает CRLF).
// Хвостовые пробелы и табуляции нарочно сохраняются: это часть запроса
// (например, блок кода, вставленный с отступом).
func trimEOL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\r' || s[len(s)-1] == '\n') {
		s = s[:len(s)-1]
	}
	return s
}
