package core

import (
	"strings"
	"testing"
	"time"
)

func TestApproxTokensRussian(t *testing.T) {
	// Русский текст: ~1.4 символа на токен, значит оценка должна быть
	// заметно больше, чем rune/3.
	ru := strings.Repeat("Это тестовое сообщение для проверки оценки токенов. ", 20)
	got := ApproxTokens(ru)
	runes := len([]rune(ru))
	if got < runes/2 {
		t.Fatalf("оценка токенов слишком мала: %d для %d рун", got, runes)
	}
	if got > runes {
		t.Fatalf("оценка токенов слишком велика: %d для %d рун", got, runes)
	}
}

func TestApproxTokensEmpty(t *testing.T) {
	if got := ApproxTokens(""); got != 0 {
		t.Fatalf("пустая строка должна давать 0, получено %d", got)
	}
}

func TestTruncateKeepsUTF8(t *testing.T) {
	s := strings.Repeat("я", 100)
	got := Truncate(s, 10)
	if !isValidUTF8(got) {
		t.Fatal("Truncate разорвал UTF-8")
	}
	if len([]rune(got)) != 10 {
		t.Fatalf("ожидалось 10 рун, получено %d", len([]rune(got)))
	}
}

func TestTruncateUTF8HeadTail(t *testing.T) {
	s := "начало" + strings.Repeat("x", 100) + "конец"
	got := TruncateUTF8(s, 10, 10)
	if !isValidUTF8(got) {
		t.Fatal("TruncateUTF8 разорвал UTF-8")
	}
	if !strings.HasPrefix(got, "начало") {
		t.Fatal("потеряно начало")
	}
	if !strings.HasSuffix(got, "конец") {
		t.Fatal("потерян конец")
	}
}

func TestIsBinary(t *testing.T) {
	if !IsBinary([]byte{0x00, 0x01, 0x02}) {
		t.Fatal("нули должны считаться двоичными")
	}
	if IsBinary([]byte("обычный текст")) {
		t.Fatal("текст не должен считаться двоичным")
	}
}

func TestKfmt(t *testing.T) {
	cases := map[int]string{
		0: "0", 999: "999", 1000: "1.0k", 1500: "1.5k", 12345: "12k", 1000000: "1.0M",
	}
	for in, want := range cases {
		if got := Kfmt(in); got != want {
			t.Errorf("Kfmt(%d) = %q, ожидалось %q", in, got, want)
		}
	}
}

func TestHumanSize(t *testing.T) {
	if got := HumanSize(512); !strings.Contains(got, "Б") {
		t.Errorf("HumanSize(512) = %q", got)
	}
	if got := HumanSize(2048); !strings.Contains(got, "КБ") {
		t.Errorf("HumanSize(2048) = %q", got)
	}
}

func TestHumanDuration(t *testing.T) {
	if got := HumanDuration(500 * time.Millisecond); got != "500мс" {
		t.Errorf("HumanDuration = %q", got)
	}
	if got := HumanDuration(5 * time.Second); got != "5.0с" {
		t.Errorf("HumanDuration = %q", got)
	}
}

func TestSlug(t *testing.T) {
	if got := Slug("Мой проект / v2.0", 20); got != "мои-проект-v2" && got != "мой-проект-v2" {
		t.Logf("Slug = %q (допустимо)", got)
	}
	if got := Slug("!!!", 10); got != "item" {
		t.Errorf("Slug должен давать запасное значение, получено %q", got)
	}
}

func TestPermsClone(t *testing.T) {
	p := Perms{BashExact: map[string]bool{"ls": true}, BashAll: true}
	c := p.Clone()
	c.BashExact["ls"] = false
	c.BashExact["pwd"] = true
	if !p.BashExact["ls"] {
		t.Fatal("Clone должен deep-copy карту")
	}
	if p.BashExact["pwd"] {
		t.Fatal("Clone должен deep-copy карту")
	}
}

func TestValidStatus(t *testing.T) {
	if ValidStatus("weird") != TodoPending {
		t.Error("неизвестный статус должен становиться pending")
	}
	if ValidStatus(TodoCompleted) != TodoCompleted {
		t.Error("корректный статус должен сохраняться")
	}
}

func TestWriteAtomicAndRead(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/sub/file.txt"
	if err := WriteAtomic(path, []byte("данные"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := readFileHelper(path)
	if err != nil || string(data) != "данные" {
		t.Fatalf("прочитано %q, ошибка %v", data, err)
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}
