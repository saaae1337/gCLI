package tui

import "testing"

// Тесты X10-мыши: терминалы без SGR-мыши (1006) шлют ESC [ M b x y —
// три сырых байта со смещением 32. Раньше парсер съедал только заголовок,
// а координаты уходили в поле ввода как руны: консоль Linux при каждом
// клике и прокрутке засоряла composer печатным мусором.

func TestParseX10MouseWheel(t *testing.T) {
	// Колесо вверх: кнопка 64 (96 со смещением), колонка 10 (42), строка 5 (37).
	ev, n, need := parseKey([]byte("\x1b[M`*%"), false)
	if need {
		t.Fatal("полная X10-последовательность помечена как неполная")
	}
	if n != 6 {
		t.Fatalf("съедено %d байт, хочу 6 (ESC [ M и три байта данных)", n)
	}
	if ev.Wheel != -1 || ev.X != 10 || ev.Y != 5 {
		t.Fatalf("событие %+v, хочу Wheel=-1 X=10 Y=5", ev)
	}
}

func TestParseX10MouseWheelDown(t *testing.T) {
	// Колесо вниз: кнопка 65 (97), колонка 3 (35), строка 2 (34).
	ev, _, _ := parseKey([]byte("\x1b[Ma#\""), false)
	if ev.Wheel != 1 || ev.X != 3 || ev.Y != 2 {
		t.Fatalf("событие %+v, хочу Wheel=1 X=3 Y=2", ev)
	}
}

func TestParseX10MouseClickNoWheel(t *testing.T) {
	// Клик (кнопка 0): координаты приходят, колесо — нет; событие ни на
	// что не влияет, но и не должно превращаться в руны в поле ввода.
	ev, n, _ := parseKey([]byte("\x1b[M !\""), false)
	if n != 6 {
		t.Fatalf("съедено %d байт, хочу 6", n)
	}
	if ev.Wheel != 0 || ev.X != 1 || ev.Y != 2 {
		t.Fatalf("событие %+v, хочу Wheel=0 X=1 Y=2", ev)
	}
}

func TestParseX10MouseIncompleteWaits(t *testing.T) {
	// Двух байтов данных мало: парсер ждёт продолжение, а по форсу
	// отступает к Esc, как и для других недописанных последовательностей.
	_, _, need := parseKey([]byte("\x1b[M`*"), false)
	if !need {
		t.Fatal("неполная X10-последовательность не помечена как неполная")
	}
	ev, n, _ := parseKey([]byte("\x1b[M`*"), true)
	if ev.Key != KeyEsc || n != 1 {
		t.Fatalf("форс дал %+v (n=%d), хочу Esc (n=1)", ev, n)
	}
}

func TestParseSGRMouseStillWorks(t *testing.T) {
	// SGR-мышь (основной путь современных терминалов) не должна
	// пострадать: < в параметрах уводит в старую ветку.
	ev, n, need := parseKey([]byte("\x1b[<64;10;5M"), false)
	if need || n != 11 {
		t.Fatalf("SGR: need=%v n=%d, хочу false/11", need, n)
	}
	if ev.Wheel != -1 || ev.X != 10 || ev.Y != 5 {
		t.Fatalf("SGR событие %+v, хочу Wheel=-1 X=10 Y=5", ev)
	}
}
