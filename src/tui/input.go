package tui

import (
	"bytes"
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
	// Вставка: от ESC[200~ до ESC[201~ всё — текст. Вставка длиннее
	// одного чтения приходит кусками; пауза между ними — не повод
	// рубить её на Esc: спорный Esc прерывал текущий ход, а остаток
	// вставки печатался в поле ввода посимвольно, включая хвост [201~.
	pasteStart := []byte("\x1b[200~")
	pasteEnd := []byte("\x1b[201~")
	pasteOpen := false
	emitPaste := func(text []byte) bool {
		t := strings.ReplaceAll(string(text), "\r\n", "\n")
		t = strings.ReplaceAll(t, "\r", "\n")
		return emit(Event{Paste: true, Text: t})
	}
	// markerTail — длина хвоста p, которая может быть началом закрывающего
	// маркера вставки ESC[201~, разрезанного между чтениями. На медленном
	// канале (SSH, большой буфер обмена) пауза между кусками легко превышает
	// 250 мс: хвост «\x1b[2» уезжал в текст вставки, а его продолжение
	// «01~» приходило следом отдельной вставкой-мусором в поле ввода.
	markerTail := func(p []byte) int {
		// Заготовка короче целого маркера, поэтому потолок — len(pasteEnd)-1;
		// цельный маркер уже поглощён веткой pasteOpen выше.
		m := len(p)
		if m > len(pasteEnd)-1 {
			m = len(pasteEnd) - 1
		}
		for k := m; k > 0; k-- {
			if bytes.Equal(p[len(p)-k:], pasteEnd[:k]) {
				return k
			}
		}
		return 0
	}
	for {
		// Пока в буфере есть полные события — отдаём их без задержки.
		for len(pending) > 0 {
			if pasteOpen {
				// Внутри вставки разбираем только закрывающий маркер.
				if end := bytes.Index(pending, pasteEnd); end >= 0 {
					if !emitPaste(pending[:end]) {
						return
					}
					pending = pending[end+len(pasteEnd):]
					pasteOpen = false
					continue
				}
				break // ждём продолжение вставки
			}
			if bytes.HasPrefix(pending, pasteStart) {
				rest := pending[len(pasteStart):]
				if end := bytes.Index(rest, pasteEnd); end >= 0 {
					if !emitPaste(rest[:end]) {
						return
					}
					pending = rest[end+len(pasteEnd):]
				} else {
					pending = rest
					pasteOpen = true
					break
				}
				continue
			}
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
			// Вставке ждём дольше: её куски следуют друг за другом, и
			// пауза между ними — не признак одиночного Esc.
			wait := 35 * time.Millisecond
			if pasteOpen {
				wait = 250 * time.Millisecond
			}
			timer := time.NewTimer(wait)
			select {
			case chunk, ok = <-raw:
			case <-timer.C:
				if pasteOpen {
					// Хвост вставки не спешит: отдаём накопленное как
					// вставку и продолжаем ждать закрытие. Хвост, похожий
					// на начало разрезанного маркера, остаётся в pending:
					// это не текст вставки, в поле ввода ему делать нечего.
					cut := len(pending) - markerTail(pending)
					if cut > 0 && !emitPaste(pending[:cut]) {
						timer.Stop()
						return
					}
					pending = pending[cut:]
					timer.Stop()
					continue
				}
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
			// Поток кончился: ESC без продолжения — клавиша Esc,
			// незакрытая вставка — вставка без закрывающего маркера.
			if pasteOpen {
				// Поток оборвался посреди маркера: заготовку ESC[201~
				// в текст вставки не отдаём.
				if cut := len(pending) - markerTail(pending); cut > 0 {
					emitPaste(pending[:cut])
				}
				return
			}
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
	case 0x1b:
		// Двойное Esc (два нажатия в одном чтении) раньше уходило в
		// ветку «Alt+руна» ниже: 0x1b — валидная руна, и оба нажатия
		// съедало одно Alt-событие, которое обработчики молча бросали
		// (case ev.Alt: return). Прерывание хода, закрытие модалки и
		// очистка ввода по Esc не срабатывали, пока нажатия не разнесены
		// форс-таймером. Отдаём первое нажатие как Esc, второе разберёт
		// следующая итерация цикла.
		return Event{Key: KeyEsc}, 1, false
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
		if b[fin] == 'M' && !strings.HasPrefix(seq, "<") {
			// X10-мышь: ESC [ M b x y — три сырых байта. Раньше съедался
			// только заголовок, а координаты уходили в поле ввода рунами:
			// терминалы без SGR-мыши (например, консоль Linux с 1000h, но
			// без 1006h) засоряли composer при каждом клике и прокрутке.
			if len(b) < next+3 {
				if forced {
					return Event{Key: KeyEsc}, 1, false
				}
				return Event{}, 0, true
			}
			return parseX10Mouse(b[next], b[next+1], b[next+2]), next + 3, false
		}
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

// parseX10Mouse — старый формат X10: код кнопки и координаты со смещением 32.
// Битовая раскладка та же, что у SGR: 64/65 — колесо, 4/8/16 — Shift/Alt/Ctrl.
func parseX10Mouse(btn, xb, yb byte) Event {
	b := int(btn) - 32
	x := int(xb) - 32
	y := int(yb) - 32
	if b < 0 || x < 0 || y < 0 {
		return Event{}
	}
	ev := Event{X: x, Y: y}
	switch b & 0x43 {
	case 64: // колесо вверх
		ev.Wheel = -1
	case 65: // колесо вниз
		ev.Wheel = 1
	}
	if b&4 != 0 {
		ev.Shift = true
	}
	if b&8 != 0 {
		ev.Alt = true
	}
	if b&16 != 0 {
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
