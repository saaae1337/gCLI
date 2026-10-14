package ui

import "testing"

// Решение о псевдографике на Windows держится на кодовой странице
// консоли, а не на локали. Проверяется чистая функция: настоящую консоль
// в тесте не создать, а решение обязано быть проверяемым.
func TestConsoleCanShowUnicode(t *testing.T) {
	cases := []struct {
		name      string
		outCP     int
		canSwitch bool
		want      bool
	}{
		// Консоли нет: вывод ушёл в файл или пайп. Спрашивать нечего,
		// иначе тесты и CI сочли бы терминал непригодным.
		{"консоли нет", 0, false, true},

		// Уже UTF-8 — ничего трогать не надо.
		{"уже utf-8", 65001, false, true},
		{"уже utf-8, переключение доступно", 65001, true, true},

		// CP866 — умолчание русской Windows. Псевдографику печатать
		// можно только после переключения: «│» в CP866 читается как «тФВ».
		{"cp866, переключили", 866, true, true},
		// Переключиться не дали — ASCII, а не кракозябры.
		{"cp866, переключение запрещено", 866, false, false},
		{"cp1251, переключение запрещено", 1251, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := consoleCanShowUnicode(c.outCP, c.canSwitch); got != c.want {
				t.Errorf("кодовая страница %d, переключение %v: получили %v, ждали %v",
					c.outCP, c.canSwitch, got, c.want)
			}
		})
	}
}

// Ключевая проверка на Windows: русская консоль CP866 обязана давать
// псевдографику, потому что gcli сама переводит консоль в UTF-8.
// Иначе весь интерфейс пользователя рассыпается в «тФВ».
func TestConsoleCanShowUnicodeSwitchesFromRussianCodepage(t *testing.T) {
	if !consoleCanShowUnicode(866, true) {
		t.Fatal("с переключением консоли псевдографика обязана быть доступна")
	}
}

// ConsoleUTF8 обязан отвечать, не падая, и не должен трогать консоль,
// когда вывод ушёл не на терминал.
func TestConsoleUTF8WithoutTTYDoesNotSwitch(t *testing.T) {
	// Вывод в файл или пайп: UTF-8 и так правильный ответ, а кодовую
	// страницу консоли пользователя менять незачем — он этого не просил.
	if !ConsoleUTF8(false) {
		t.Error("в файл или пайп UTF-8 писать можно всегда")
	}
}

func TestConsoleUTF8DoesNotPanic(t *testing.T) {
	// Под тестом настоящей консоли нет, важно лишь, что функция не падает.
	_ = ConsoleUTF8(true)
}
