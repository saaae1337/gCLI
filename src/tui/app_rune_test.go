package tui

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Регрессии байтово-рунного рассогласования: comp.Text() отдаёт строку
// (байты), а comp.Cursor() считает руны. На кириллице срез t[:cur]
// резал строку посреди руны — @-дополнение молча не работало после
// русского текста, подстановка пути портила поле ввода, а обрезка
// аргумента в строке активности рисовала мусорный символ.

// TestAtTokenCyrillic — @-токен после русского текста должен находиться.
func TestAtTokenCyrillic(t *testing.T) {
	app := newTestApp(strings.NewReader(""), &bytes.Buffer{})
	app.mu.Lock()
	defer app.mu.Unlock()

	app.comp.SetText("прочитай @al")
	tok, ok := app.atToken()
	if !ok {
		t.Fatal("atToken не нашёл @-токен после кириллицы")
	}
	if tok != "al" {
		t.Fatalf("токен %q, ожидался \"al\"", tok)
	}

	// Курсор не в конце: токен перед курсором, хвост игнорируется.
	app.comp.SetText("прочитай @al остаток")
	for i := 0; i < 8; i++ { // курсор сразу после «@al»
		app.comp.Left()
	}
	tok, ok = app.atToken()
	if !ok || tok != "al" {
		t.Fatalf("токен у курсора: %q, ok=%v", tok, ok)
	}
}

// TestCompleteFileCyrillic — подстановка пути не должна портить русский
// текст в поле ввода: результат обязан быть валидным UTF-8 и точным.
func TestCompleteFileCyrillic(t *testing.T) {
	app := newTestApp(strings.NewReader(""), &bytes.Buffer{})
	app.mu.Lock()
	defer app.mu.Unlock()

	app.comp.SetText("прочитай @al")
	app.files = []string{"alpha.go"}
	app.filesAt = time.Now()

	if !app.completeFileLocked() {
		t.Fatal("подстановка не сработала после кириллицы")
	}
	got := app.comp.Text()
	want := "прочитай @alpha.go "
	if got != want {
		t.Fatalf("текст %q, ожидался %q", got, want)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("невалидный UTF-8 после подстановки: %q", got)
	}
}

// TestToolActivityCutRuneSafe — обрезка длинного аргумента не должна
// разрезать руну: «a» + 30 кириллических «ы» режется по байту 42,
// который попадает внутрь двубайтовой руны.
func TestToolActivityCutRuneSafe(t *testing.T) {
	arg := "a" + strings.Repeat("ы", 30)
	got := toolActivity(ToolInfo{Name: "bash", Args: arg})
	if !utf8.ValidString(got) {
		t.Fatalf("невалидный UTF-8 в строке активности: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("обрезка без многоточия: %q", got)
	}
	// Байт 42 — второй байт двадцатой руны: обрезка отступает к 41,
	// в результате 20 полных «ы» после «a».
	want := "bash a" + strings.Repeat("ы", 20) + "…"
	if got != want {
		t.Fatalf("неожиданный текст: %q", got)
	}
}
