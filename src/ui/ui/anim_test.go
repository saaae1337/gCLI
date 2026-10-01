package ui

import (
	"strings"
	"testing"
)

// animUI — UI с включёнными анимациями для проверки эффектов появления.
func animUI(width int) (*UI, *strings.Builder) {
	buf := &strings.Builder{}
	u := New(Options{
		Theme: themeEmber, Unicode: true, Color: true, Grade: ColorRGB,
		Animations: true, Width: width, Out: buf,
	})
	return u, buf
}

// afterMark — часть вывода после последнего кадра «\r ESC[K».
// Нужна, чтобы смотреть именно на содержимое финального кадра:
// StripANSI умеет только последовательности, оканчивающиеся на «m»,
// и проглатывает «ESC[K» вместе со всем, что после него.
func afterMark(out string) string {
	mark := "\r\033[K"
	i := strings.LastIndex(out, mark)
	if i < 0 {
		return ""
	}
	return out[i+len(mark):]
}

func TestRevealLinesPrintsAllLines(t *testing.T) {
	u, buf := animUI(80)
	lines := []string{"первая строка", "вторая строка", "третья строка"}
	u.RevealLines(lines)
	out := buf.String()

	for _, l := range lines {
		if !strings.Contains(StripANSI(out), l) {
			t.Errorf("строка %q не появилась в выводе", l)
		}
	}
	// Проявление: сначала приглушённый кадр, потом обычный.
	if !strings.Contains(out, "\r\033[K") {
		t.Errorf("ожидалась перерисовка строк при проявлении:\n%q", firstRunes(out))
	}
	// Финальный кадр — последняя строка, после блока перевод строки:
	// иначе следующий вывод дописался бы в последнюю строку.
	if got := StripANSI(afterMark(out)); got != "третья строка\n" {
		t.Errorf("финальный кадр должен быть последней строкой, получено %q", got)
	}
}

func TestRevealLinesFallsBackWithoutAnimation(t *testing.T) {
	buf := &strings.Builder{}
	u := New(Options{
		Theme: themeEmber, Unicode: true, Color: true, Grade: ColorRGB,
		Animations: false, Width: 80, Out: buf,
	})
	lines := []string{"одна", "две"}
	u.RevealLines(lines)
	out := buf.String()

	if strings.Contains(out, "\033[") {
		t.Errorf("без анимации не должно быть управляющих кодов:\n%q", out)
	}
	if got := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; got != len(lines) {
		t.Errorf("напечатано %d строк, ожидалось %d", got, len(lines))
	}
}

func TestTypeLinePrintsWholeString(t *testing.T) {
	u, buf := animUI(80)
	u.TypeLine("проверка появления")
	out := buf.String()

	if got := StripANSI(afterMark(out)); got != "проверка появления" {
		t.Errorf("финальный кадр = %q, ожидалась вся строка", got)
	}
}

func TestTypeLineSkipsLongStrings(t *testing.T) {
	u, buf := animUI(80)
	long := strings.Repeat("я", revealMaxChars+1)
	u.TypeLine(long)
	out := buf.String()

	// Слишком длинная строка печатается сразу, без перерисовок.
	if strings.Contains(out, "\r\033[K") {
		t.Error("слишком длинная строка не должна анимироваться")
	}
	if got := StripANSI(out); got != long+"\n" {
		t.Errorf("длинная строка должна печататься целиком, получено %d символов", runeLen(got))
	}
}

func TestRevealLineDimsWholeColoredLine(t *testing.T) {
	// Строка приходит уже раскрашенной, и каждый её фрагмент заканчивается
	// сбросом \033[0m. Затухание должно действовать на ВСЕ фрагменты:
	// иначе оно гаснет на первом же переходе и эффекта не видно.
	u, buf := animUI(80)
	colored := u.c(u.pal.OK, "✔") + " " + u.Gray("готово")
	u.RevealLine("  " + colored)
	out := buf.String()

	k := strings.Index(out, "\r\033[K")
	if k < 0 {
		t.Fatalf("не найден кадр проявления: %q", out)
	}
	rest := out[k+len("\r\033[K"):]
	dim := rest[:strings.Index(rest, "\r\033[K")]

	// Сколько сбросов — столько же раз должен вернуться dim.
	resets := strings.Count(dim, cReset)
	dims := strings.Count(dim, cDim)
	if dims < resets {
		t.Errorf("dim вернулся %d раз при %d сбросах цвета — затухание гаснет на первом фрагменте:\n%q",
			dims, resets, dim)
	}
}

func TestDimFrameNoColorPassthrough(t *testing.T) {
	// Без цвета в строке затухание просто оборачивает её целиком.
	if got := dimFrame("просто текст"); got != cDim+"просто текст"+cReset {
		t.Errorf("неожиданный кадр: %q", got)
	}
}

func TestDimFrameKeepsColors(t *testing.T) {
	// dim добавляется и после последнего сброса — чтобы прикрыть хвост
	// строки, где цвет уже погас. Главное: текст и исходные цвета на месте.
	s := "\033[38;2;1;2;3mтекст\033[0m"
	got := dimFrame(s)
	if !strings.Contains(got, "текст") {
		t.Errorf("текст потерялся: %q", got)
	}
	if !strings.Contains(got, "38;2;1;2;3m") {
		t.Errorf("исходный цвет фрагмента должен сохраниться: %q", got)
	}
}

func TestDimFrameWithoutResets(t *testing.T) {
	// Строка без единого сброса оборачивается ровно одним dim.
	s := "\033[38;2;1;2;3mтекст"
	got := dimFrame(s)
	if strings.Count(got, cDim) != 1 {
		t.Errorf("dim должен быть один, а не %d: %q", strings.Count(got, cDim), got)
	}
}

func TestRevealLinesEndsWithNewline(t *testing.T) {
	// Без перевода строки в конце курсор остаётся на последней строке
	// блока, и следующий вывод дописывается в неё.
	u, buf := animUI(80)
	u.RevealLines([]string{"одна", "две"})
	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("после блока должен быть перевод строки: %q", out)
	}
}

// TestRevealLineEndsWithNewline — строка проявления обязана кончаться
// переводом строки. Без него следующий вывод дописывался бы в конец
// проявленной строки: результат инструмента слипался с закрывающей
// строкой рельса (она печатается следом).
func TestRevealLineEndsWithNewline(t *testing.T) {
	u, buf := animUI(80)
	u.RevealLine("  строка результата")
	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("после строки должен быть перевод строки: %q", out)
	}
	// Ровно один перевод: ни заготовок, ни лишних строк быть не должно.
	if n := strings.Count(out, "\n"); n != 1 {
		t.Errorf("ожидался один перевод строки, получено %d: %q", n, out)
	}
	if got := StripANSI(afterMark(out)); got != "  строка результата\n" {
		t.Errorf("финальный кадр = %q", got)
	}
}

// TestRevealLinesDimsColoredLines — строки баннера и рельса приходят уже
// раскрашенными. Затухание обёрткой приглушённого цвета гасло бы на первом
// же сбросе внутри строки, поэтому применяется dimFrame.
func TestRevealLinesDimsColoredLines(t *testing.T) {
	u, buf := animUI(80)
	u.RevealLines([]string{u.c(u.pal.OK, "✔") + " " + u.Gray("готово")})
	out := buf.String()

	k := strings.Index(out, "\033[K")
	if k < 0 {
		t.Fatalf("не найден кадр проявления: %q", out)
	}
	frame := out[k+len("\033[K"):]
	frame = frame[:strings.Index(frame, "\r\033[K")]

	if resets := strings.Count(frame, cReset); strings.Count(frame, cDim) < resets {
		t.Errorf("dim вернулся %d раз при %d сбросах цвета — затухание гаснет на первом фрагменте:\n%q",
			strings.Count(frame, cDim), resets, frame)
	}
}

// TestRevealLinesAnimatedOnlyWhenAllowed — анимация требует цвета, юникода,
// терминала и непустого режима вывода. В пайпе и при -no-color строки
// печатаются как есть.
func TestRevealLinesAnimatedOnlyWhenAllowed(t *testing.T) {
	buf := &strings.Builder{}
	u := New(Options{
		Theme: themeEmber, Unicode: true, Color: false, Grade: ColorNone,
		Animations: true, Width: 80, Out: buf,
	})
	u.RevealLines([]string{"одна", "две"})
	if strings.Contains(buf.String(), "\033[") {
		t.Errorf("без цвета управления курсором быть не должно: %q", buf.String())
	}
}

// TestRevealLinesNoDoubleAnimation — если анимация выключена, строки печатаются
// по одной, без заготовки: блок не должен «прыгать» на экране.
func TestRevealLinesNoDoubleAnimation(t *testing.T) {
	buf := &strings.Builder{}
	u := New(Options{
		Theme: themeEmber, Unicode: true, Color: true, Grade: ColorRGB,
		Animations: false, Width: 80, Out: buf,
	})
	u.RevealLines([]string{"одна", "две"})
	out := buf.String()
	if got := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; got != 2 {
		t.Errorf("напечатано %d строк, ожидалось 2", got)
	}
}

// TestRailEndChainStopsWaitLine — закрывающая строка рельса гасит строку
// ожидания (кот «working...») и печатается чисто: без управления курсором
// в обычном режиме и без хвоста ожидания после себя.
func TestRailEndChainStopsWaitLine(t *testing.T) {
	u, buf := animUI(80)
	u.RailEndChain("готово")
	out := buf.String()

	if !strings.Contains(StripANSI(out), "готово") {
		t.Errorf("текст закрывающей строки должен сохраниться: %q", firstRunes(out))
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("после закрывающей строки должен быть перевод строки: %q", firstRunes(out))
	}
}

func TestAnimationsOffInQuietMode(t *testing.T) {
	u, buf := animUI(80)
	u.SetQuiet(true)
	u.RevealLines([]string{"одна"})
	if strings.Contains(buf.String(), "\r\033[K") {
		t.Errorf("в quiet-режиме анимаций быть не должно: %q", buf.String())
	}
}

func TestAnimationsOffWithoutColor(t *testing.T) {
	buf := &strings.Builder{}
	u := New(Options{
		Theme: themeEmber, Unicode: true, Color: false, Grade: ColorNone,
		Animations: true, Width: 80, Out: buf,
	})
	u.RevealLine("строка")
	if strings.Contains(buf.String(), "\033[") {
		t.Errorf("без цвета перерисовка не нужна: %q", buf.String())
	}
}
