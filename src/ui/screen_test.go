package ui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

// newTestScreen - UI с включённым полноэкранным режимом, но без тикера:
// перерисовка только синхронная (screenPaintSync), размер фиксированный.
// Так тесты детерминированы и свободны от гонок с фоновой перерисовкой.
func newTestScreen(t *testing.T, w, h int) (*UI, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	u := New(Options{
		Out: &buf, Unicode: true, Color: false,
		Width: w, Height: h,
	})
	u.mu.Lock()
	u.scr = &Screen{active: true, stop: make(chan struct{}), wrapW: -1}
	u.screenOn.Store(true)
	u.mu.Unlock()
	t.Cleanup(func() { u.ScreenStop() })
	return u, &buf
}

// screenPaintSync - синхронная перерисовка кадра (для тестов).
func (u *UI) screenPaintSync() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.paintScreenLocked()
}

// scrLines - снимок транскрипта из-под блокировки.
func scrLines(u *UI) []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]string, len(u.scr.lines))
	for i, l := range u.scr.lines {
		out[i] = l.text
	}
	return out
}

// Строки рамки поля ввода - ровно шириной в кадр: правая граница не
// уезжает и не отстаёт на любой ширине окна.
func TestScreenBoxRowsFitWidth(t *testing.T) {
	for _, w := range []int{12, 20, 30, 60, 80, 200} {
		u, _ := newTestScreen(t, w, 10)
		u.mu.Lock()
		u.liveInp = "почини падающий тест в пакете ui и объясни, что происходило"
		u.liveCur = len([]rune(u.liveInp))
		rows, _, _ := u.screenBoxRows(w)
		u.mu.Unlock()
		for i, r := range rows {
			if n := cellWidth(r); n != w {
				t.Errorf("ширина %d: строка %d рамки = %d колонок: %q", w, i, n, StripANSI(r))
			}
		}
		if rows[0][0] != ' ' {
			t.Errorf("рамка должна начинаться с отступа, получено %q", rows[0])
		}
	}
}

// Курсор - настоящий курсор терминала: позиция считается от рамки.
func TestScreenBoxCaret(t *testing.T) {
	u, _ := newTestScreen(t, 40, 10)
	u.mu.Lock()
	u.liveInp = "привет мир"
	u.liveCur = 3
	rows, cr, cc := u.screenBoxRows(40)
	u.mu.Unlock()
	if cr != 1 {
		t.Errorf("курсор в строке %d, ожидался 1 (первая строка ввода)", cr)
	}
	if cc != textCol()+3 {
		t.Errorf("курсор в колонке %d, ожидалось %d", cc, textCol()+3)
	}
	if len(rows) != 3 {
		t.Errorf("одна строка ввода = 3 строки рамки, получено %d", len(rows))
	}

	// Многострочный ввод: курсор в конце второй строки.
	u.mu.Lock()
	u.liveInp = "ab\ncd"
	u.liveCur = 5
	_, cr2, cc2 := u.screenBoxRows(40)
	u.mu.Unlock()
	if cr2 != 2 || cc2 != textCol()+2 {
		t.Errorf("многострочный ввод: курсор в (%d,%d), ожидался (2,%d)", cr2, cc2, textCol()+2)
	}

	// Пустое поле с плейсхолдером: курсор в начало текста.
	u.mu.Lock()
	u.liveInp = ""
	u.livePlaceholder = `Try "/help"`
	_, cr3, cc3 := u.screenBoxRows(40)
	u.mu.Unlock()
	if cr3 != 1 || cc3 != textCol() {
		t.Errorf("плейсхолдер: курсор в (%d,%d), ожидался (1,%d)", cr3, cc3, textCol())
	}
}

// Смена ширины окна - транскрипт перелицовывается целиком: после resize
// в стенке не остаётся строк шире нового окна.
func TestScreenReflowOnResize(t *testing.T) {
	u, _ := newTestScreen(t, 40, 12)
	long := "длинная строка транскрипта, которая заведомо шире, чем тридцать колонок окна терминала"
	u.Write(long + "\n")
	u.screenPaintSync()

	u.SetWidth(30)
	u.screenPaintSync() // кадр под новую ширину
	u.screenPaintSync() // и ещё один: кэш перелицовки уже под 30

	u.mu.Lock()
	defer u.mu.Unlock()
	if u.scr.wrapW != 30 {
		t.Fatalf("wrapW = %d, ожидалось 30", u.scr.wrapW)
	}
	if len(u.scr.lines) != 1 {
		t.Fatalf("строк в транскрипте %d, ожидалась 1", len(u.scr.lines))
	}
	for _, r := range u.scr.lines[0].rows {
		if n := cellWidth(r); n > 30 {
			t.Errorf("после resize строка %d колонок (лимит 30): %q", n, r)
		}
	}
	if len(u.scr.lines[0].rows) < 2 {
		t.Error("длинная строка должна разбиться на несколько после сужения")
	}
}

// Строка ожидания - формат Claude Code: «esc to interrupt», метка, время.
func TestScreenBusyLine(t *testing.T) {
	u, buf := newTestScreen(t, 60, 12)
	u.mu.Lock()
	u.spin = true
	u.spinT0 = time.Now().Add(-4 * time.Second)
	u.liveBusy = "Thinking"
	u.liveMark = "|"
	busy := u.liveBusyLine(60)
	u.mu.Unlock()

	plain := StripANSI(busy)
	if !strings.Contains(plain, "esc to interrupt") {
		t.Errorf("в строке ожидания нет «esc to interrupt»: %q", plain)
	}
	if !strings.Contains(plain, "Thinking") {
		t.Errorf("в строке ожидания нет метки: %q", plain)
	}
	if !strings.Contains(plain, "4s") {
		t.Errorf("в строке ожидания нет времени: %q", plain)
	}

	u.screenPaintSync()
	if !strings.Contains(StripANSI(buf.String()), "esc to interrupt") {
		t.Error("строка ожидания не попала в кадр")
	}
}

// Прокрутка зажимается: PgUp не уводит вьюпорт дальше начала стенки.
func TestScreenScrollClamps(t *testing.T) {
	u, _ := newTestScreen(t, 40, 12)
	for i := 0; i < 50; i++ {
		u.Write(fmt.Sprintf("строка %d\n", i))
	}
	total := len(scrLines(u))
	if total != 50 {
		t.Fatalf("строк %d, ожидалось 50", total)
	}
	u.ScreenScroll(1000)
	u.ScreenScroll(1000)
	u.mu.Lock()
	got := u.scr.scroll
	u.mu.Unlock()
	if got > total {
		t.Errorf("scroll = %d при %d строках", got, total)
	}
	u.ScreenScroll(-1000)
	u.mu.Lock()
	if u.scr.scroll != 0 {
		t.Errorf("scroll = %d, ожидался 0", u.scr.scroll)
	}
	u.mu.Unlock()
}

// Незавершённая строка ждёт продолжения: печать без перевода строки
// не должна рвать транскрипт на части.
func TestScreenPendingTail(t *testing.T) {
	u, _ := newTestScreen(t, 40, 10)
	u.Write("часть строки без перевода")
	u.mu.Lock()
	if len(u.scr.lines) != 0 || u.scr.pending != "часть строки без перевода" {
		t.Errorf("хвост потерялся: строк %d, pending=%q", len(u.scr.lines), u.scr.pending)
	}
	u.mu.Unlock()

	u.Write(" и продолжение\n")
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.scr.pending != "" {
		t.Errorf("хвост не склеился: %q", u.scr.pending)
	}
	if len(u.scr.lines) != 1 || u.scr.lines[0].text != "часть строки без перевода и продолжение" {
		t.Errorf("строка склеилась неверно: %v", scrLines(u))
	}
}

// Вход и выход из альтернативного буфера: терминал обязан быть
// восстановлен, даже если сессия оборвалась.
func TestScreenEnterLeave(t *testing.T) {
	var buf bytes.Buffer
	u := New(Options{Out: &buf, Unicode: true, Color: false, Width: 40, Height: 10})
	screenTestForce = true
	defer func() { screenTestForce = false }()

	if !u.ScreenStart() {
		t.Fatal("полноэкранный режим не включился")
	}
	if !strings.HasPrefix(buf.String(), "\x1b[?1049h") {
		t.Errorf("нет входа в альтернативный буфер: %q", buf.String())
	}
	// Печать идёт в транскрипт (появится в кадре), а не мимо.
	u.Println("привет")
	if len(scrLines(u)) != 1 {
		t.Errorf("строка не попала в транскрипт: %v", scrLines(u))
	}
	u.ScreenStop()
	if !strings.HasSuffix(buf.String(), "\x1b[?25h\x1b[2J\x1b[?1049l") {
		t.Errorf("нет выхода из альтернативного буфера: %q", buf.String())
	}
	if u.ScreenActive() {
		t.Error("режим должен быть выключен после ScreenStop")
	}
}

// Слишком малый терминал: кадр не должен паниковать или рисовать мусор -
// нижняя зона сжимается от наименее важного к важному.
func TestScreenTinyTerminal(t *testing.T) {
	u, buf := newTestScreen(t, 14, 5)
	for i := 0; i < 20; i++ {
		u.Write(fmt.Sprintf("строка %d\n", i))
	}
	u.screenPaintSync()
	out := buf.String()
	if strings.Contains(out, "panic") {
		t.Error("кадр на малом терминале не должен паниковать")
	}
	// Рамка поля ввода всегда рисуется.
	if !strings.Contains(StripANSI(out), "╭") || !strings.Contains(StripANSI(out), "╰") {
		t.Errorf("поле ввода исчезло на малом терминале:\n%s", StripANSI(out))
	}
}

// Экран слишком мал даже для рамки: остаётся голая строка ввода.
func TestScreenMicroTerminal(t *testing.T) {
	u, _ := newTestScreen(t, 12, 2)
	for i := 0; i < 5; i++ {
		u.Write(fmt.Sprintf("строка %d\n", i))
	}
	u.screenPaintSync() // не должно паниковать
}
