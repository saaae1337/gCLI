package tools

import (
	"strings"
	"testing"
)

// ---------- Автономный прогон в self_status ----------

func TestSelfReportShowsMission(t *testing.T) {
	// Модель обязана видеть прогон: без этого она на длинной дистанции
	// не отличает «меня просили продолжить четыре раза» от «меня никто
	// не трогал» и продолжает одно и то же по кругу.
	rep := SelfReport{
		SystemPrompt: "тест",
		Model:        "test",
		Mission:      "время: 1ч0м0с из 4h · итерации: 40 из 200 · вызовы инструментов: 55",
	}
	txt := rep.Text()
	if !strings.Contains(txt, "Автономный прогон") {
		t.Errorf("в отчёте нет раздела о прогоне:\n%s", txt)
	}
	if !strings.Contains(txt, "вызовы инструментов: 55") {
		t.Errorf("строка прогона не попала в отчёт:\n%s", txt)
	}
	// Раздел обязан быть вырезаемым: Section() работает по заголовкам,
	// и именно «сколько раз меня просили продолжить» модель спрашивает
	// чаще всего.
	sec := rep.Section("прогон")
	if strings.Contains(sec, "Контекст:") {
		t.Errorf("section «прогон» вернул не только свой блок:\n%s", sec)
	}
	if !strings.Contains(sec, "вызовы инструментов") {
		t.Errorf("section «прогон» пуст:\n%s", sec)
	}
}

func TestSelfReportWithoutMission(t *testing.T) {
	// Обычный ход не должен получать пустой заголовок прогона: раздел
	// «Автономный прогон» без содержимого учит модель, что прогона
	// не бывает.
	rep := SelfReport{SystemPrompt: "тест", Model: "test"}
	txt := rep.Text()
	if strings.Contains(txt, "Автономный прогон") {
		t.Errorf("у обычного хода не должно быть раздела о прогоне:\n%s", txt)
	}
}

// ---------- Согласование ----------

func TestPluralRU(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{1, "раз"}, {2, "раза"}, {5, "раз"},
		{11, "раз"}, {12, "раз"}, {14, "раз"},
		{21, "раз"}, {22, "раза"}, {25, "раз"},
		{101, "раз"}, {111, "раз"},
	}
	for _, c := range cases {
		if got := pluralRU(c.n, "раз", "раза", "раз"); got != c.want {
			t.Errorf("pluralRU(%d) = %q, ждали %q", c.n, got, c.want)
		}
	}
}

func TestContextBarFills(t *testing.T) {
	if _, pct := contextBar(50, 100); pct != 50 {
		t.Errorf("заполнено на %d%%, ждали 50", pct)
	}
	// Больше порога не заливается: 120% показывать нельзя, иначе
	// арифметика в процентах врёт.
	if _, pct := contextBar(120, 100); pct != 100 {
		t.Errorf("переполнение показано как %d%%, ждали 100", pct)
	}
	if _, pct := contextBar(10, 0); pct != 0 {
		t.Errorf("без порога процент должен быть нулевым, ждали 0, получили %d", pct)
	}
}

// ---------- Разделы отчёта ----------

func TestSelfReportSectionUnknownFallsBack(t *testing.T) {
	// Неизвестный раздел возвращает весь отчёт, а не пустоту: модель,
	// спросившая «сколько токенов», должна получить хоть что-то.
	rep := SelfReport{SystemPrompt: "тест", Model: "test"}
	if got := rep.Section("такого раздела нет"); got != rep.Text() {
		t.Error("неизвестный раздел должен возвращать отчёт целиком")
	}
	if got := rep.Section(""); got != rep.Text() {
		t.Error("пустой раздел должен возвращать отчёт целиком")
	}
}

func TestSelfReportToolLimitsAlwaysPresent(t *testing.T) {
	// Лимиты инструментов — то, из-за чего модель впустую тратит вызовы.
	// Их absence ломает весь отчёт, а не украшает его.
	rep := SelfReport{SystemPrompt: "тест", Model: "test"}
	txt := rep.Text()
	for _, tool := range []string{"read_file", "self_status", "spawn_agent"} {
		if !strings.Contains(txt, tool) {
			t.Errorf("в лимитах нет %s", tool)
		}
	}
}
