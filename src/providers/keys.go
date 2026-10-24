package providers

import (
	"strings"
	"sync"
	"time"
)

// ---------- Пул ключей: переключение на лету ----------

// Ключ API может кончиться в середине работы: лимит запросов, исчерпанный
// баланс, отзыв ключа. Если у провайдера есть запасной ключ, смена должна
// происходить сама и незаметно: человек не должен перезапускать ход из-за
// того, что у первого ключа кончился лимит.

// Паузы ключей.
const (
	// KeyPauseLimit — пауза после лимита запросов: лимит обычно
	// восстанавливается через минуту.
	KeyPauseLimit = 60 * time.Second
	// KeyPauseNoMoney — пауза после сообщения о пустом балансе: деньги
	// пополняют вручную, и ждать тут бессмысленно.
	KeyPauseNoMoney = 30 * time.Minute
	// KeyPauseAuth — пауза после отказа в доступе: ключ невалиден или
	// отозван, но проверить это можно только позже.
	KeyPauseAuth = 10 * time.Minute
)

// KeyStatus — состояние ключа в пуле (для показа в интерфейсе).
type KeyStatus struct {
	// Index — номер ключа в пуле (с единицы).
	Index int
	// Masked — замаскированный ключ.
	Masked string
	// Current — ключ используется прямо сейчас.
	Current bool
	// Paused — ключ на паузе (лимит или баланс).
	Paused bool
	// Invalid — ключ отвергнут провайдером.
	Invalid bool
	// Until — до какого момента ключ на паузе.
	Until time.Time
	// Reason — почему ключ отложен.
	Reason string
}

// KeyPool — пул ключей провайдера с переключением на лету.
//
// Потокобезопасен: субагенты работают параллельно и делят пуль провайдера,
// а переключение ключа происходит из потока сетевого запроса.
type KeyPool struct {
	mu sync.Mutex
	// keys — ключи провайдера по порядку. Первый — основной.
	keys []string
	// idx — текущий ключ.
	idx int
	// until — до какого момента ключ на паузе.
	until map[string]time.Time
	// bad — ключ, отвергнутый провайдером (401/403).
	bad map[string]string
	// reason — почему ключ отложен (показывается в интерфейсе).
	reason map[string]string
}

// NewKeyPool — пул из списка ключей. Пустой список — пустой пул.
func NewKeyPool(keys []string) *KeyPool {
	p := &KeyPool{
		until:  map[string]time.Time{},
		bad:    map[string]string{},
		reason: map[string]string{},
	}
	p.SetKeys(keys)
	return p
}

// SetKeys — заменить список ключей, сохранив состояние.
//
// Состояние нужно сохранить по значению ключа, а не по номеру: ключи
// добавляют и убирают, и после такой правки пауза должна остаться на
// прежнем месте, а не переехать на другой ключ.
func (p *KeyPool) SetKeys(keys []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	clean := make([]string, 0, len(keys))
	seen := map[string]bool{}
	for _, k := range keys {
		k = CleanKey(k)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		clean = append(clean, k)
	}
	p.keys = clean
	if p.idx >= len(clean) {
		p.idx = 0
	}
	// Забываем состояние ключей, которых больше нет.
	for k := range p.until {
		if !seen[k] {
			delete(p.until, k)
			delete(p.reason, k)
		}
	}
	for k := range p.bad {
		if !seen[k] {
			delete(p.bad, k)
		}
	}
}

// Keys — список ключей (копия).
func (p *KeyPool) Keys() []string {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.keys...)
}

// Len — сколько ключей в пуле.
func (p *KeyPool) Len() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.keys)
}

// Add — добавить ключ в пул. false, если такой уже есть.
func (p *KeyPool) Add(key string) bool {
	key = CleanKey(key)
	if key == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, k := range p.keys {
		if k == key {
			return false
		}
	}
	p.keys = append(p.keys, key)
	return true
}

// Remove — убрать ключ из пула.
func (p *KeyPool) Remove(key string) bool {
	key = CleanKey(key)
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, k := range p.keys {
		if k == key {
			p.keys = append(p.keys[:i], p.keys[i+1:]...)
			delete(p.until, key)
			delete(p.bad, key)
			delete(p.reason, key)
			if p.idx >= len(p.keys) {
				p.idx = 0
			}
			return true
		}
	}
	return false
}

// Use — сделать ключ текущим (основным).
func (p *KeyPool) Use(key string) bool {
	key = CleanKey(key)
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, k := range p.keys {
		if k == key {
			p.idx = i
			// Явный выбор снимает паузу: человек сказал, что ключ годен.
			delete(p.until, key)
			delete(p.bad, key)
			delete(p.reason, key)
			return true
		}
	}
	return false
}

// Reset — снять паузы и вернуть в строй отвергнутые ключи.
func (p *KeyPool) Reset() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.until = map[string]time.Time{}
	p.bad = map[string]string{}
	p.reason = map[string]string{}
	p.idx = 0
}

// Current — ключ, который пойдёт в запрос.
//
// Если пригодных ключей нет, берётся тот, чья пауза кончится раньше
// всего: с ним шанс, что ключ уже ожил. Молча ждать было бы хуже.
func (p *KeyPool) Current() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.currentLocked()
}

func (p *KeyPool) currentLocked() string {
	if len(p.keys) == 0 {
		return ""
	}
	now := time.Now()
	for i := 0; i < len(p.keys); i++ {
		idx := (p.idx + i) % len(p.keys)
		k := p.keys[idx]
		if p.bad[k] != "" {
			continue
		}
		if until, ok := p.until[k]; ok && now.Before(until) {
			continue
		}
		p.idx = idx
		return k
	}
	// Все ключи недоступны: берём тот, что оживёт раньше.
	best := -1
	var bestUntil time.Time
	for i, k := range p.keys {
		if p.bad[k] != "" {
			continue
		}
		until := p.until[k]
		if best < 0 || until.Before(bestUntil) {
			best, bestUntil = i, until
		}
	}
	if best < 0 {
		best = 0
	}
	p.idx = best
	return p.keys[best]
}

// Rotate — текущий ключ не годится: уходит на паузу или выбрасывается.
//
// Возвращает номер нового ключа и признак, что переключиться удалось.
// Ключей больше нет — возвращается false, и вызывающий отдаёт ошибку
// человеку: делать вид, что запрос ушёл, нельзя.
func (p *KeyPool) Rotate(reason string, pause time.Duration, invalid bool) (from, to int, ok bool) {
	if p == nil {
		return 0, 0, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.keys) == 0 {
		return 0, 0, false
	}
	from = p.idx + 1
	cur := p.keys[p.idx]
	if invalid {
		p.bad[cur] = reason
	} else {
		if pause <= 0 {
			pause = KeyPauseLimit
		}
		p.until[cur] = time.Now().Add(pause)
	}
	p.reason[cur] = reason
	now := time.Now()
	for i := 1; i <= len(p.keys); i++ {
		idx := (p.idx + i) % len(p.keys)
		k := p.keys[idx]
		if p.bad[k] != "" {
			continue
		}
		if until, ok := p.until[k]; ok && now.Before(until) {
			continue
		}
		p.idx = idx
		return from, idx + 1, true
	}
	return from, 0, false
}

// PausedUntil — до какого момента ключи недоступны (пустой пул — нет).
//
// Когда все ключи на паузе, человеку надо сказать, когда ждать: иначе
// он видит «лимит запросов» и не понимает, что делать.
func (p *KeyPool) PausedUntil() (time.Time, bool) {
	if p == nil {
		return time.Time{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var earliest time.Time
	found := false
	for k := range p.until {
		until := p.until[k]
		if !found || until.Before(earliest) {
			earliest, found = until, true
		}
	}
	return earliest, found
}

// Status — состояние всех ключей пула.
func (p *KeyPool) Status() []KeyStatus {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	out := make([]KeyStatus, 0, len(p.keys))
	for i, k := range p.keys {
		st := KeyStatus{Index: i + 1, Masked: MaskKey(k), Current: i == p.idx}
		if reason, ok := p.bad[k]; ok {
			st.Invalid = true
			st.Reason = reason
		} else if until, ok := p.until[k]; ok && now.Before(until) {
			st.Paused = true
			st.Until = until
			st.Reason = p.reason[k]
		}
		out = append(out, st)
	}
	return out
}

// IsCurrent — этот ли ключ сейчас активен.
func (p *KeyPool) IsCurrent(key string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idx >= len(p.keys) {
		return false
	}
	return p.keys[p.idx] == key
}

// SplitKeys — разобрать список ключей из строки: запятые, точки с
// запятой, пробелы и переводы строк.
//
// Экспорт: список ключей вводят и в конфиг, и в команду REPL, и в
// переменную окружения — разбор должен быть один.
func SplitKeys(s string) []string { return splitKeys(s) }

// splitKeys — разобрать список ключей из строки: запятые, точки с
// запятой, пробелы и переводы строк.
func splitKeys(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	}) {
		if k := CleanKey(part); k != "" {
			out = append(out, k)
		}
	}
	return out
}
