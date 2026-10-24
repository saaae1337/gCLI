package providers

// Тесты пула ключей: переключение на лету, паузы и возврат в строй.

import (
	"testing"
	"time"
)

func TestPoolRotatesOnLimit(t *testing.T) {
	p := NewKeyPool([]string{"key1", "key2", "key3"})
	if got := p.Current(); got != "key1" {
		t.Fatalf("активный ключ = %q, ожидался key1", got)
	}
	from, to, ok := p.Rotate("лимит запросов", KeyPauseLimit, false)
	if !ok {
		t.Fatal("переключиться не удалось")
	}
	if from != 1 || to != 2 {
		t.Errorf("переключение %d→%d, ожидалось 1→2", from, to)
	}
	if got := p.Current(); got != "key2" {
		t.Errorf("активный ключ = %q, ожидался key2", got)
	}
	// Исходный ключ на паузе: вернуться на него раньше времени нельзя.
	p.idx = 0
	if got := p.Current(); got != "key2" {
		t.Errorf("паузный ключ снова стал активным: %q", got)
	}
}

func TestPoolRejectsInvalidKey(t *testing.T) {
	p := NewKeyPool([]string{"bad", "good"})
	from, to, ok := p.Rotate("ключ отклонён", 0, true)
	if !ok || from != 1 || to != 2 {
		t.Fatalf("переключение %d→%d ok=%v, ожидалось 1→2", from, to, ok)
	}
	// Отвергнутый ключ больше не выбирается никогда.
	for i := 0; i < 5; i++ {
		if got := p.Current(); got == "bad" {
			t.Fatal("отвергнутый ключ снова активен")
		}
		p.Rotate("лимит запросов", KeyPauseLimit, false)
	}
}

func TestPoolSingleKeyCannotRotate(t *testing.T) {
	p := NewKeyPool([]string{"only"})
	_, _, ok := p.Rotate("лимит запросов", KeyPauseLimit, false)
	if ok {
		t.Error("переключиться не на что")
	}
	// Ключ при этом уходит на паузу, и пул об этом знает.
	if _, paused := p.PausedUntil(); !paused {
		t.Error("ключ не поставлен на паузу")
	}
}

func TestPoolAllPausedPicksEarliest(t *testing.T) {
	p := NewKeyPool([]string{"a", "b"})
	p.until["a"] = time.Now().Add(time.Hour)
	p.until["b"] = time.Now().Add(time.Minute)
	p.idx = 0
	// Все ключи на паузе: берётся тот, кто оживёт раньше.
	if got := p.Current(); got != "b" {
		t.Errorf("выбран ключ %q, ожидался b (пауза короче)", got)
	}
}

func TestPoolSetKeysKeepsState(t *testing.T) {
	p := NewKeyPool([]string{"a", "b"})
	p.Rotate("лимит запросов", KeyPauseLimit, false) // a на паузе
	p.SetKeys([]string{"b", "c"})
	// Состояние осталось на прежнем месте: у a паузы уже нет (ключа нет),
	// а b — рабочий.
	statuses := p.Status()
	if len(statuses) != 2 {
		t.Fatalf("ключей в пуле %d, ожидалось 2", len(statuses))
	}
	if statuses[0].Masked != MaskKey("b") || statuses[1].Masked != MaskKey("c") {
		t.Errorf("состав пула неверный: %+v", statuses)
	}
}

func TestPoolAddRemoveUse(t *testing.T) {
	p := NewKeyPool(nil)
	if !p.Add("one") {
		t.Error("ключ не добавился")
	}
	if p.Add("one") {
		t.Error("дубль добавился")
	}
	p.Add("two")
	if !p.Use("two") {
		t.Error("ключ не стал основным")
	}
	if got := p.Current(); got != "two" {
		t.Errorf("активный ключ = %q, ожидался two", got)
	}
	if !p.Remove("two") {
		t.Error("ключ не убрался")
	}
	if p.Len() != 1 {
		t.Errorf("в пуле %d ключей, ожидался 1", p.Len())
	}
}

func TestPoolResetClearsPauses(t *testing.T) {
	p := NewKeyPool([]string{"a", "b"})
	p.Rotate("лимит запросов", KeyPauseLimit, false)
	p.Reset()
	if got := p.Current(); got != "a" {
		t.Errorf("после сброса активен %q, ожидался a", got)
	}
}

func TestSplitKeysParsesList(t *testing.T) {
	got := splitKeys("a, b;c\nd")
	if len(got) != 4 {
		t.Fatalf("разобрано %d ключей, ожидалось 4: %v", len(got), got)
	}
	if got[0] != "a" || got[3] != "d" {
		t.Errorf("ключи разобраны неверно: %v", got)
	}
}

func TestProviderCurrentKeyFromPool(t *testing.T) {
	p := &Provider{ID: "test", Kind: ProtoOpenAI, Pool: NewKeyPool([]string{"k1", "k2"})}
	if got := p.CurrentKey(); got != "k1" {
		t.Errorf("активный ключ = %q, ожидался k1", got)
	}
	if p.KeyCount() != 2 {
		t.Errorf("ключей %d, ожидалось 2", p.KeyCount())
	}
	if !p.HasKey() {
		t.Error("провайдер с ключами считается без ключа")
	}
	// Без пула работает обычный одиночный ключ.
	single := &Provider{ID: "one", Key: "sk-1"}
	if single.CurrentKey() != "sk-1" || single.KeyCount() != 1 {
		t.Errorf("одиночный ключ не виден: %q %d", single.CurrentKey(), single.KeyCount())
	}
}

func TestProviderFindsKeyListInEnv(t *testing.T) {
	t.Setenv("MYPROV_API_KEYS", "k1,k2")
	p := &Provider{ID: "myprov", KeyEnvs: []string{"MYPROV_API_KEY"}}
	keys := p.findKeys()
	if len(keys) != 2 || keys[0] != "k1" || keys[1] != "k2" {
		t.Errorf("ключи из окружения не прочитаны: %v", keys)
	}
}

func TestQuotaReasonDetectsLimit(t *testing.T) {
	cases := []string{
		"You exceeded your current quota, please check your plan and billing details",
		"insufficient_quota",
		"余额不足",
		"请求过于频繁，限流",
	}
	for _, c := range cases {
		if _, ok := quotaReason(c, ""); !ok {
			t.Errorf("не распознан лимит: %q", c)
		}
	}
	if _, ok := quotaReason("invalid model name", "invalid_request_error"); ok {
		t.Error("обычная ошибка принята за лимит")
	}
}
