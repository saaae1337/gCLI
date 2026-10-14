package ui

// Невалидный UTF-8 в потоке вывода.
//
// Через интерфейс проходит чужой текст: вывод инструмента, содержимое
// файла, ответ модели. Файл в windows-1251 или latin-1 — обычное дело
// (выгрузка из 1С, старый .txt, .ini), и его байты, попав в UTF-8
// консоль, дают мусор на весь вывод: русский текст превращается в
// «CF F0 E8 E2», а интерфейс перестаёт быть читаемым целиком.

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

// cp1251Hello — «Привет» в windows-1251: байты не образуют корректный
// UTF-8, поэтому в поток попадать не должны.
var cp1251Hello = []byte{0xCF, 0xF0, 0xE8, 0xE2, 0xE5, 0xF2}

func newTestUI(buf *bytes.Buffer) *UI {
	return New(Options{Out: buf, Color: false, Width: 80})
}

func TestWriteRepairsInvalidUTF8(t *testing.T) {
	var buf bytes.Buffer
	u := newTestUI(&buf)
	u.Write(string(cp1251Hello) + " мир")

	got := buf.String()
	if !utf8.ValidString(got) {
		t.Fatalf("в поток ушёл невалидный UTF-8: % x", buf.Bytes())
	}
	if strings.Contains(got, "П") {
		t.Error("байты windows-1251 не должны превращаться в кириллицу: " +
			"их нельзя угадать, молча подставлять русские буквы — значит врать")
	}
	// Знак замены должен стоять на месте потерянных символов: он прямо
	// показывает пользователю, где текст сломан. Молчаливый пропуск
	// склеивает слова и выглядит как «ппивет».
	if !strings.ContainsRune(got, utf8.RuneError) {
		t.Errorf("нет знака замены на месте битых байтов: %q", got)
	}
	// Хвост после битых байтов обязан уцелеть.
	if !strings.HasSuffix(got, " мир") {
		t.Errorf("текст после битых байтов потерян: %q", got)
	}
}

func TestWriteLeavesValidUTF8Untouched(t *testing.T) {
	for _, s := range []string{
		"", "обычный текст", "✔ готово", "🧡 оранжевое",
		"\x1b[31mкрасный\x1b[0m", "日本語", "👨‍👩‍👧‍👦",
	} {
		var buf bytes.Buffer
		u := newTestUI(&buf)
		u.Write(s)
		if got := buf.String(); got != s {
			t.Errorf("валидная строка изменилась при выводе:\n  было  %q\n  стало %q", s, got)
		}
	}
}

// Путь не должен обходить починку: спиннер, маскот и прогресс пишут в
// поток напрямую, минуя Write.
func TestAllOutputPathsRepairUTF8(t *testing.T) {
	var buf bytes.Buffer
	u := newTestUI(&buf)
	bad := string(cp1251Hello)

	u.Write(bad)
	u.RawWrite(bad)
	u.put(bad) // низкоуровневая точка, которой пользуются все пути

	if !utf8.ValidString(buf.String()) {
		t.Fatalf("невалидный UTF-8 ушёл через один из путей: % x", buf.Bytes())
	}
}

// Обрезка не должна ни убирать, ни плодить знаки замены.
func TestTruncateDoesNotCorruptValidText(t *testing.T) {
	s := "Привет, мир"
	got := truncateCells(s, 6)
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Errorf("обрезка испортила валидный текст: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("обрезка дала невалидный UTF-8: %q", got)
	}
}
