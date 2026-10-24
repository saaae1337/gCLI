package core

import (
	"fmt"
	"sync"
	"unicode/utf8"
)

// Capture — сборщик вывода процесса с потолком памяти.
//
// Зачем: bash-инструмент раньше читал весь вывод через CombinedOutput, и
// команда, выплёвывающая гигабайт лога, укладывала процесс — а модель всё
// равно видела только первые 16 тысяч символов. Capture хранит первые и
// последние limit байт потока, а середину считает и заменяет строкой
// «пропущено …»: памяти всегда O(limit), а модель по-прежнему видит и
// начало (заголовки, первые ошибки), и конец (итог, код выхода).
//
// Потокобезопасен: exec.Cmd с одним и тем же писателем в Stdout и Stderr
// обычно сводит их в одну трубу, но на гарантию полагаться нельзя.
type Capture struct {
	mu    sync.Mutex
	head  []byte
	ring  []byte
	rpos  int
	rfill int
	total int64
	limit int
}

// NewCapture — сборщик с потолком limit байт на голову и на хвост.
func NewCapture(limit int) *Capture {
	if limit < 256 {
		limit = 256
	}
	return &Capture{limit: limit, ring: make([]byte, limit)}
}

// Write — реализация io.Writer.
func (c *Capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total += int64(len(p))
	if len(c.head) < c.limit {
		take := c.limit - len(c.head)
		if take > len(p) {
			take = len(p)
		}
		c.head = append(c.head, p[:take]...)
	}
	for _, b := range p {
		c.ring[c.rpos] = b
		c.rpos++
		if c.rpos == len(c.ring) {
			c.rpos = 0
		}
		if c.rfill < len(c.ring) {
			c.rfill++
		}
	}
	return len(p), nil
}

// Total — сколько всего байт прошло через сборщик.
func (c *Capture) Total() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

// String — собранный текст: начало и конец потока, середина (если была)
// заменена строкой «пропущено N». Обрезанные руны по краям кусков
// срезаются: иначе сломанный байт на границе окна превращался бы в знак
// замены посреди читаемого вывода.
func (c *Capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.total <= int64(len(c.ring)) {
		// Всё уместилось: кольцо хранит поток целиком, по порядку.
		return trimRuneEdges(string(c.ring[:c.rfill]))
	}
	var tail []byte
	if c.rfill == len(c.ring) {
		tail = append(tail, c.ring[c.rpos:]...)
		tail = append(tail, c.ring[:c.rpos]...)
	} else {
		tail = append(tail, c.ring[:c.rfill]...)
	}
	dropped := c.total - int64(len(c.head)) - int64(len(tail))
	return trimRuneEdges(string(c.head)) +
		fmt.Sprintf("\n…[вывод длиннее %s — пропущено %s]…\n",
			HumanSize(c.limit), HumanSize(int(dropped))) +
		trimRuneEdges(string(tail))
}

// trimRuneEdges — срезать недокрученные UTF-8-последовательности с обоих
// концов строки.
func trimRuneEdges(s string) string {
	b := []byte(s)
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size <= 1 {
			b = b[size:]
			continue
		}
		break
	}
	for len(b) > 0 {
		r, size := utf8.DecodeLastRune(b)
		if r == utf8.RuneError && size <= 1 {
			b = b[:len(b)-size]
			continue
		}
		break
	}
	return string(b)
}
