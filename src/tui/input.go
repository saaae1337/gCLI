package tui

import (
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ---------- Ввод ----------
//
// Терминал отдаёт байты, а интерфейсу нужны события: руны, стрелки,
// колесо мыши, вставка. Разбор живёт в одной горутине, поэтому порядок
// нажатий сохраняется, а приложение получает готовый поток.

// Key — клавиша без руны.
type Key int

const (
	KeyRune Key = iota // обычный символ (см. Event.R)
	KeyEnter
	KeyTab
	KeyBacktab
	KeyBackspace
	KeyDelete
	KeyEsc
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPgUp
	KeyPgDn
	KeyInsert
)

// Event — событие ввода.
type Event struct {
	Key   Key
	R     rune
	Text  string // для вставки
	Ctrl  bool
	Alt   bool
	Shift bool

	Paste bool  // вставка из буфера обмена
	Wheel int   // -1 вверх, +1 вниз
	X, Y  int   // координаты мыши (1-based)
	Tick  int64 // служебное: тик анимации (в события не попадает)
}

// Reader — читатель терминала.
type Reader struct {
	in   io.Reader
	out  chan Event
	stop chan struct{}
	once sync.Once
}

// NewReader — запустить разбор ввода.
func NewReader(in io.Reader) *Reader {
	r := &Reader{
		in:   in,
		out:  make(chan Event, 128),
		stop: make(chan struct{}),
	}
	go r.loop()
	return r
}

// Events — канал событий.
func (r *Reader) Events() <-chan Event { return r.out }

// Close — остановить читателя.
func (r *Reader) Close() {
	r.once.Do(func() { close(r.stop) })
}

// loop — читать байты и разбирать их на события.
func (r *Reader) loop() {
	defer close(r.out)
	raw := make(chan []byte, 8)
	go func() {
		defer close(raw)
		buf := make([]byte, 4096)
		for {
			n, err := r.in.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				select {
				case raw <- chunk:
				case <-r.stop:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	var pending []byte
	emit := func(ev Event) bool {
		select {
		case r.out <- ev:
			return true
		case <-r.stop:
			return false
		}
	}
	for {
		// Пока в буфере есть полные события — отдаём их без задержки.
		for len(pending) > 0 {
			ev, n, need := parseKey(pending, false)
			if n > 0 {
				if !emit(ev) {
					return
				}
				pending = pending[n:]
				continue
			}
			if need {
				break
			}
			// Непонятный байт: пропускаем, чтобы не зациклиться.
			pending = pending[1:]
		}
		var chunk []byte
		var ok bool
		if len(pending) > 0 {
			// Неполная последовательность: ждём продолжение недолго —
			// одиночный Esc должен срабатывать сразу, а не после Enter.
			timer := time.NewTimer(35 * time.Millisecond)
			select {
			case chunk, ok = <-raw:
			case <-timer.C:
				ev, n, _ := parseKey(pending, true)
				if n > 0 {
					if !emit(ev) {
						return
					}
					pending = pending[n:]
				} else {
					pending = pending[1:]
				}
				timer.Stop()
				continue
			case <-r.stop:
				timer.Stop()
				return
			}
			timer.Stop()
		} else {
			select {
			case chunk, ok = <-raw:
			case <-r.stop:
				return
			}
		}
		if !ok {
			// Поток кончился: ESC без продолжения — клавиша Esc.
			if len(pending) > 0 {
				ev, n, _ := parseKey(pending, true)
				if n > 0 {
					emit(ev)
				}
			}
			return
		}
		pending = append(pending, chunk...)
	}
}

// parseKey — разобрать одно событие в начале буфера.
//
// n>0 — событие разобрано, need=true — данных пока мало (не трогать
// буфер, подождать ещё). forced=true — времени больше нет: незакрытую
// последовательность трактуем как Esc.
func parseKey(b []byte, forced bool) (ev Event, n int, need bool) {
	if len(b) == 0 {
		return Event{}, 0, true
	}
	switch b[0] {
	case 0x1b:
		return parseEscape(b, forced)
	case '\r':
		return Event{Key: KeyEnter}, 1, false
	case '\n':
		// Ctrl+J: перевод строки внутри запроса. Enter приходит как \r.
		return Event{Key: KeyRune, R: 'j', Ctrl: true}, 1, false
	case '\t':
		return Event{Key: KeyTab}, 1, false
	case 0x7f, 0x08:
		return Event{Key: KeyBackspace}, 1, false
	}
	if b[0] < 0x20 {
		// Ctrl+<буква>: 0x01..0x1a. Ctrl+Space — 0x00.
		if b[0] == 0 {
			return Event{Key: KeyRune, R: ' ', Ctrl: true}, 1, false
		}
		return Event{Key: KeyRune, R: rune('a' + b[0] - 1), Ctrl: true}, 1, false
	}
	if b[0] < utf8.RuneSelf {
		return Event{Key: KeyRune, R: rune(b[0])}, 1, false
	}
	if !utf8.FullRune(b) {
		return Event{}, 0, true
	}
	r, size := utf8.DecodeRune(b)
	if r == utf8.RuneError && size == 1 {
		return Event{}, 0, false
	}
	return Event{Key: KeyRune, R: r}, size, false
}

// parseEscape — ESC-последовательности.
func parseEscape(b []byte, forced bool) (Event, int, bool) {
	if len(b) == 1 {
		if forced {
			return Event{Key: KeyEsc}, 1, false
		}
		return Event{}, 0, true
	}
	switch b[1] {
	case '[':
		return parseCSI(b, forced)
	case 'O':
		if len(b) < 3 {
			if forced {
				return Event{Key: KeyEsc}, 1, false
			}
			return Event{}, 0, true
		}
		switch b[2] {
		case 'A':
			return Event{Key: KeyUp}, 3, false
		case 'B':
			return Event{Key: KeyDown}, 3, false
		case 'C':
			return Event{Key: KeyRight}, 3, false
		case 'D':
			return Event{Key: KeyLeft}, 3, false
		case 'H':
			return Event{Key: KeyHome}, 3, false
		case 'F':
			return Event{Key: KeyEnd}, 3, false
		}
		return Event{}, 3, false
	case '\r':
		return Event{Key: KeyEnter, Alt: true}, 2, false
	}
	// Alt+руна.
	r, size := utf8.DecodeRune(b[1:])
	if r == utf8.RuneError && size == 1 {
		return Event{Key: KeyEsc}, 1, false
	}
	return Event{Key: KeyRune, R: r, Alt: true}, 1 + size, false
}

// parseCSI — CSI-последовательность: ESC [ параметры финал.
func parseCSI(b []byte, forced bool) (Event, int, bool) {
	// Ищем финальный байт (0x40..0x7e).
	fin := -1
	for i := 2; i < len(b); i++ {
		if b[i] >= 0x40 && b[i] <= 0x7e {
			fin = i
			break
		}
	}
	if fin < 0 {
		if forced {
			return Event{Key: KeyEsc}, 1, false
		}
		return Event{}, 0, true
	}
	seq := string(b[2:fin])
	next := fin + 1
	switch b[fin] {
	case 'A', 'B', 'C', 'D', 'H', 'F':
		var k Key
		switch b[fin] {
		case 'A':
			k = KeyUp
		case 'B':
			k = KeyDown
		case 'C':
			k = KeyRight
		case 'D':
			k = KeyLeft
		case 'H':
			k = KeyHome
		default:
			k = KeyEnd
		}
		ev := Event{Key: k}
		// «1;5F» — Ctrl+End: модификатор важен для прокрутки ленты.
		if i := strings.IndexByte(seq, ';'); i >= 0 {
			if mod, err := strconv.Atoi(seq[i+1:]); err == nil {
				applyMods(&ev, mod)
			}
		}
		return ev, next, false
	case 'Z':
		return Event{Key: KeyBacktab}, next, false
	case '~':
		// Вставка из буфера: ESC[200~ ... ESC[201~.
		if seq == "200" {
			rest := b[next:]
			end := strings.Index(string(rest), "\x1b[201~")
			if end < 0 {
				if forced {
					return Event{Key: KeyEsc}, 1, false
				}
				return Event{}, 0, true
			}
			// Вставка из Windows-буфера приходит с CRLF: в поле ввода
			// перевод строки один, иначе \r попал бы в текст запроса.
			text := strings.ReplaceAll(string(rest[:end]), "\r\n", "\n")
			text = strings.ReplaceAll(text, "\r", "\n")
			return Event{Paste: true, Text: text}, next + end + len("\x1b[201~"), false
		}
		code := seq
		if i := strings.IndexByte(code, ';'); i >= 0 {
			code = code[:i]
		}
		switch code {
		case "1", "7":
			return Event{Key: KeyHome}, next, false
		case "4", "8":
			return Event{Key: KeyEnd}, next, false
		case "2":
			return Event{Key: KeyInsert}, next, false
		case "3":
			return Event{Key: KeyDelete}, next, false
		case "5":
			return Event{Key: KeyPgUp}, next, false
		case "6":
			return Event{Key: KeyPgDn}, next, false
		}
		return Event{}, next, false
	case 'u':
		// Kitty-протокол: ESC[13;2u — Shift+Enter.
		parts := strings.Split(seq, ";")
		if parts[0] == "13" {
			ev := Event{Key: KeyEnter}
			if len(parts) > 1 {
				mod, _ := strconv.Atoi(parts[1])
				applyMods(&ev, mod)
			}
			return ev, next, false
		}
		return Event{}, next, false
	case 'M', 'm':
		return parseMouse(seq, next), next, false
	}
	return Event{}, next, false
}

// parseMouse — SGR-мышь.
func parseMouse(seq string, n int) Event {
	if !strings.HasPrefix(seq, "<") {
		return Event{}
	}
	parts := strings.Split(strings.TrimPrefix(seq, "<"), ";")
	if len(parts) < 3 {
		return Event{}
	}
	btn, _ := strconv.Atoi(parts[0])
	x, _ := strconv.Atoi(parts[1])
	y, _ := strconv.Atoi(parts[2])
	ev := Event{X: x, Y: y}
	switch btn & 0x43 {
	case 64: // колесо вверх
		ev.Wheel = -1
	case 65: // колесо вниз
		ev.Wheel = 1
	}
	if btn&4 != 0 {
		ev.Shift = true
	}
	if btn&8 != 0 {
		ev.Alt = true
	}
	if btn&16 != 0 {
		ev.Ctrl = true
	}
	return ev
}

// applyMods — разобрать модификаторы xterm (1 + Shift 1, Alt 2, Ctrl 4).
func applyMods(ev *Event, mod int) {
	if mod <= 0 {
		return
	}
	m := mod - 1
	ev.Shift = m&1 != 0
	ev.Alt = m&2 != 0
	ev.Ctrl = m&4 != 0
}
