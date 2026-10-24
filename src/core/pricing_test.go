package core

import (
	"strings"
	"testing"
)

// TestPricePickLongestIsDeterministic — выбор цены по подстроке обязан
// давать один и тот же ответ при одном и том же имени модели.
//
// Победителем становился самый длинный подошедший ключ, а при равной
// длине — первый попавшийся, а обход map в Go случаен. Пока в таблице
// нет двух равных по длине ключей, совпадающих с одной моделью, это
// латентно; но стоит добавить в цену один новый ключ — и расход в
// /stats начнёт прыгать от запуска к запуску на одной и той же
// сессии, а маршрутизация моделей станет выбирать разное при одной
// конфигурации.
func TestPricePickLongestIsDeterministic(t *testing.T) {
	table := map[string]Price{
		"alpha-1": {In: 1, Out: 1},
		"beta-11": {In: 2, Out: 2},
	}
	const model = "zz-alpha-1-beta-11zz"
	// Оба ключа содержатся в имени и оба длиной 10.
	if len("alpha-1") != len("beta-11") {
		t.Fatal("фикстура сломана: ключи должны быть равной длины")
	}
	first, ok := pickPrice(table, model)
	if !ok {
		t.Fatal("цена не найдена")
	}
	for i := 0; i < 200; i++ {
		got, ok := pickPrice(table, model)
		if !ok || got != first {
			t.Fatalf("выбор нестабилен: сначала %+v, потом %+v (ok=%v)", first, got, ok)
		}
	}
}

// TestPricePickLongestWins — при разной длине побеждает длинный ключ,
// даже если короткий встретился раньше.
func TestPricePickLongestWins(t *testing.T) {
	table := map[string]Price{
		"glm":        {In: 9, Out: 9},
		"glm-4.6":    {In: 1, Out: 1},
		"glm-4.6-vl": {In: 2, Out: 2},
	}
	p, ok := pickPrice(table, "z-ai/glm-4.6-vl-preview")
	if !ok || p.In != 2 {
		t.Errorf("ожидался самый длинный ключ glm-4.6-vl, получено %+v (ok=%v)", p, ok)
	}
}

// TestPricePickEmptyModel — пустое имя модели не должно находить первую
// попавшуюся цену: strings.Contains("", key) истинно для любого ключа.
func TestPricePickEmptyModel(t *testing.T) {
	table := map[string]Price{"glm-4.6": {In: 1, Out: 1}}
	if _, ok := pickPrice(table, ""); ok {
		t.Error("для пустого имени модели цена найдена")
	}
	if _, ok := ModelPrice("zai", "  "); ok {
		t.Error("для пустого имени модели ModelPrice вернул цену")
	}
}

// TestPricePickNoMatch — нет совпадения, нет и цены: показывать
// прочерк честнее выдуманного числа.
func TestPricePickNoMatch(t *testing.T) {
	table := map[string]Price{"glm-4.6": {In: 1, Out: 1}}
	if _, ok := pickPrice(table, "совсем-другая-модель"); ok {
		t.Error("цена найдена для несвязанной модели")
	}
	if _, ok := pickPrice(map[string]Price{}, "любая"); ok {
		t.Error("цена найдена в пустой таблице")
	}
}

// TestModelPriceResolvesThroughVendorPrefix — «z-ai/glm-4.6» должен
// находить цену «glm-4.6», а не считаться неизвестной модельой.
func TestModelPriceResolvesThroughVendorPrefix(t *testing.T) {
	p, ok := ModelPrice("zai", "z-ai/glm-4.6")
	if !ok {
		t.Fatal("цена не найдена для модели с префиксом вендора")
	}
	direct, _ := ModelPrice("zai", "glm-4.6")
	if p != direct {
		t.Errorf("цена с префиксом %+v отличается от прямой %+v", p, direct)
	}
}

// TestModelPriceUnknownProvider — неизвестный вендор молчит, а не
// подставляет чужую цену.
func TestModelPriceUnknownProvider(t *testing.T) {
	if _, ok := ModelPrice("нет-такого", "glm-4.6"); ok {
		t.Error("цена найдена для неизвестного провайдера")
	}
}

// TestPriceTableKeysAreReachable — ключ таблицы, которого ни одна
// модель не может найти, — мёртвая запись: она молча ничего не даёт.
func TestPriceTableKeysAreReachable(t *testing.T) {
	for vendor, byModel := range priceTable {
		for key := range byModel {
			if key != strings.ToLower(key) {
				t.Errorf("%s: ключ %q в верхнем регистре — поиск идёт по нижнему", vendor, key)
			}
		}
	}
}
