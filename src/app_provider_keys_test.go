package main

// Тесты работы с ключами: пул у провайдера, переключение на лету и
// команды /provider key.

import (
	"strings"
	"testing"

	"gcli/providers"
)

// TestProviderKeyAddsPool — ключи складываются в пул, а не заменяют
// друг друга: запасные ключи нужны именно для ротации.
func TestProviderKeyAddsPool(t *testing.T) {
	a, _ := cmdApp(t)
	a.cmdProvider("add myapi https://api.myservice.ru/v1")
	a.cmdProvider("key add myapi sk-first-1234567890")

	pc := a.repo.Cfg.Providers["myapi"]
	if pc == nil {
		t.Fatal("провайдер не создан")
	}
	if len(pc.Keys) != 1 {
		t.Fatalf("ключей в пуле %d, ожидался 1: %v", len(pc.Keys), pc.Keys)
	}
	if pc.APIKey != "sk-first-1234567890" {
		t.Errorf("основной ключ не записан: %q", pc.APIKey)
	}

	// Второй ключ добавляется, первый остаётся.
	a.cmdProvider("key add myapi sk-second-1234567890")
	pc = a.repo.Cfg.Providers["myapi"]
	if len(pc.Keys) != 2 {
		t.Fatalf("ключей в пуле %d, ожидалось 2: %v", len(pc.Keys), pc.Keys)
	}
	if pc.Keys[0] != "sk-first-1234567890" {
		t.Errorf("порядок ключей нарушен: %v", pc.Keys)
	}
	// Дубль не добавляется.
	a.cmdProvider("key add myapi sk-second-1234567890")
	if len(a.repo.Cfg.Providers["myapi"].Keys) != 2 {
		t.Errorf("дубль попал в пул: %v", a.repo.Cfg.Providers["myapi"].Keys)
	}
	// Пул провайдера в реестре совпадает с конфигом.
	p := a.registry.Find("myapi")
	if p == nil || p.Pool == nil || p.Pool.Len() != 2 {
		t.Fatalf("пул в реестре не собран: %+v", p)
	}
}

// TestProviderKeyRemoveAndUse — ключ можно убрать и сделать основным.
func TestProviderKeyRemoveAndUse(t *testing.T) {
	a, _ := cmdApp(t)
	a.cmdProvider("add myapi https://api.myservice.ru/v1")
	a.cmdProvider("key add myapi sk-first-1234567890 sk-second-1234567890 sk-third-1234567890")

	a.cmdProvider("key use myapi 2")
	if got := a.repo.Cfg.Providers["myapi"].APIKey; got != "sk-second-1234567890" {
		t.Errorf("основной ключ = %q, ожидался второй", got)
	}
	if got := a.registry.Find("myapi").CurrentKey(); got != "sk-second-1234567890" {
		t.Errorf("активный ключ в реестре = %q, ожидался второй", got)
	}

	a.cmdProvider("key rm myapi 1")
	keys := a.repo.Cfg.Providers["myapi"].Keys
	if len(keys) != 2 {
		t.Fatalf("ключей после удаления %d, ожидалось 2: %v", len(keys), keys)
	}
	for _, k := range keys {
		if k == "sk-first-1234567890" {
			t.Errorf("удалённый ключ остался: %v", keys)
		}
	}
}

// TestProviderKeyLegacyFormStillWorks — старая форма «/provider key <id>
// <ключ>» должна работать: ею пользуются скрипты и привычка.
func TestProviderKeyLegacyFormStillWorks(t *testing.T) {
	a, _ := cmdApp(t)
	a.cmdProvider("add myapi https://api.myservice.ru/v1")
	a.cmdProvider("key myapi sk-legacy-1234567890")
	if got := a.repo.Cfg.Providers["myapi"].APIKey; got != "sk-legacy-1234567890" {
		t.Errorf("ключ не сохранён: %q", got)
	}
}

// TestProviderKeysListShowsPool — список пула показывает ключи и их
// состояние.
func TestProviderKeysListShowsPool(t *testing.T) {
	a, buf := cmdApp(t)
	a.cmdProvider("add myapi https://api.myservice.ru/v1")
	a.cmdProvider("key add myapi sk-first-1234567890 sk-second-1234567890")
	a.cmdProvider("keys myapi")

	out := buf.String()
	if !strings.Contains(out, "Ключи") {
		t.Errorf("нет заголовка пула:\n%s", out)
	}
	// Ключи показываются замаскированными: полный ключ в выводе — утечка.
	if strings.Contains(out, "sk-first-1234567890") {
		t.Errorf("ключ показан целиком:\n%s", out)
	}
	if !strings.Contains(out, providers.MaskKey("sk-first-1234567890")) {
		t.Errorf("замаскированный ключ не показан:\n%s", out)
	}
}

// TestProviderKeyResetClearsPause — после сброса паузы ключи снова в
// строю.
func TestProviderKeyResetClearsPause(t *testing.T) {
	a, _ := cmdApp(t)
	a.cmdProvider("add myapi https://api.myservice.ru/v1")
	a.cmdProvider("key add myapi sk-first-1234567890 sk-second-1234567890")
	p := a.registry.Find("myapi")
	if p == nil || p.Pool == nil {
		t.Fatal("пул не собран")
	}
	// Ключ уходит на паузу — как при лимите.
	p.Pool.Rotate("лимит запросов", providers.KeyPauseLimit, false)
	a.cmdProvider("key reset myapi")
	if got := p.Pool.Current(); got != "sk-first-1234567890" {
		t.Errorf("после сброса активен %q, ожидался первый", got)
	}
}
