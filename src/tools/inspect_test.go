package tools

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// ---------- inspect и read_file {escape:true} ----------

// TestEscapeLineMarksInvisible — невидимые символы становятся видимыми.
// Именно из-за невидимости модель не может собрать old_string для edit_file.
func TestEscapeLineMarksInvisible(t *testing.T) {
	got := EscapeLine("a\tb\r\u00a0c")
	for _, want := range []string{"→", "␍", "⍽"} {
		if !strings.Contains(got, want) {
			t.Errorf("в выводе нет маркера %q: %q", want, got)
		}
	}
	if strings.Contains(got, "\t") || strings.Contains(got, "\r") {
		t.Errorf("в escape-виде остались невидимые символы: %q", got)
	}
	if EscapeLine("") != "" {
		t.Error("пустая строка должна оставаться пустой")
	}
}

// TestDetectInvisibleFindsBOM — BOM ломает совпадение по первой строке.
func TestDetectInvisibleFindsBOM(t *testing.T) {
	rep := DetectInvisible("f.go", "\ufeffpackage main\n")
	if !rep.HasBOM {
		t.Error("BOM не найден")
	}
	if !strings.Contains(rep.Text(), "BOM") {
		t.Errorf("в отчёте нет упоминания BOM:\n%s", rep.Text())
	}
}

// TestDetectInvisibleCRLF — CR перед переводом строки делает old_string
// из ответа модели не совпадающим с файлом.
func TestDetectInvisibleCRLF(t *testing.T) {
	rep := DetectInvisible("f.go", "a\r\nb\r\n")
	if !rep.CRLF {
		t.Error("CRLF не найден")
	}
	if rep.CRLFCount != 2 {
		t.Errorf("CRLFCount = %d, ожидалось 2", rep.CRLFCount)
	}
	if rep.Width != 2 {
		t.Errorf("Width = %d, ожидалось 2 для CRLF", rep.Width)
	}
	if !strings.Contains(rep.Text(), "CRLF") {
		t.Errorf("в отчёте нет упоминания CRLF:\n%s", rep.Text())
	}
}

// TestDetectInvisibleTabsAndTrailing — табы и хвостовые пробелы считаются.
func TestDetectInvisibleTabsAndTrailing(t *testing.T) {
	rep := DetectInvisible("f.go", "a\tb  \nc\n")
	if rep.Tabs != 1 {
		t.Errorf("Tabs = %d, ожидалось 1", rep.Tabs)
	}
	if len(rep.Trailing) != 1 || rep.Trailing[0] != 1 {
		t.Errorf("Trailing = %v, ожидалась строка 1", rep.Trailing)
	}
	if rep.CRLF {
		t.Error("LF-файл помечен как CRLF")
	}
	if rep.Width != 1 {
		t.Errorf("Width = %d, ожидалось 1 для LF", rep.Width)
	}
}

// TestDetectInvisibleCleanFile — на чистом файле отчёт говорит, что всё в порядке.
func TestDetectInvisibleCleanFile(t *testing.T) {
	rep := DetectInvisible("f.go", "package main\n")
	if !strings.Contains(rep.Text(), "Невидимых символов не найдено") {
		t.Errorf("чистый файл не распознан как чистый:\n%s", rep.Text())
	}
}

// TestInspectShowsLineNumbersAndMarks — номера строк и маркеры на месте.
func TestInspectShowsLineNumbersAndMarks(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "f.go"), "package main\n\tfunc main() {}\n")

	r := newTestReg(t, dir)
	res := mustRun(t, r, "inspect", map[string]any{"path": "f.go"})

	if !strings.Contains(res.Text, "→") {
		t.Errorf("таб не показан:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "    1 │") {
		t.Errorf("нет нумерации строк:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "ДИАГНОСТИКИ") {
		t.Errorf("нет предупреждения, что вид диагностический:\n%s", res.Text)
	}
}

// TestInspectBySearch — поиск показывает окрестности совпадения.
func TestInspectBySearch(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "f.go"), "one\ntwo\nneedle\nfour\nfive\n")

	r := newTestReg(t, dir)
	res := mustRun(t, r, "inspect", map[string]any{"path": "f.go", "search": "needle"})
	if !strings.Contains(res.Text, "needle") {
		t.Errorf("совпадение не показано:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "two") {
		t.Errorf("окрестность не показана:\n%s", res.Text)
	}
}

// TestInspectSearchNotFound — отсутствие совпадения не роняет инструмент.
func TestInspectSearchNotFound(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "f.go"), "one\ntwo\n")

	r := newTestReg(t, dir)
	res, err := run(t, r, "inspect", map[string]any{"path": "f.go", "search": "нет-такого"})
	if err != nil {
		t.Fatalf("ошибка при отсутствии совпадения: %v", err)
	}
	if res.Error != "" {
		t.Errorf("ошибка в результате: %s", res.Error)
	}
}

// TestInspectOffsetBeyondFile — запрошенная строка за файлом: внятный ответ.
func TestInspectOffsetBeyondFile(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "f.go"), "one\ntwo\n")

	r := newTestReg(t, dir)
	res := mustRun(t, r, "inspect", map[string]any{"path": "f.go", "offset": 500})
	if !strings.Contains(res.Text, "за пределами") {
		t.Errorf("нет внятного ответа про границу файла:\n%s", res.Text)
	}
}

// TestInspectMissingFileAndPath — понятные ошибки вместо системных.
func TestInspectMissingFileAndPath(t *testing.T) {
	dir := t.TempDir()
	r := newTestReg(t, dir)

	if _, err := run(t, r, "inspect", map[string]any{"path": "нет.txt"}); err == nil {
		t.Error("ожидалась ошибка для несуществующего файла")
	}
	if _, err := run(t, r, "inspect", nil); err == nil {
		t.Error("ожидалась ошибка «укажи path»")
	}
}

// TestReadFileEscapeMatchesInspect — флаг escape у read_file даёт тот же вид,
// что и отдельный инструмент. Два входа нужны для удобства, но разный
// результат у них означал бы, что один из них врёт.
func TestReadFileEscapeMatchesInspect(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "f.go"), "package main\n\tfunc main() {}\n")

	r := newTestReg(t, dir)
	viaRead := mustRun(t, r, "read_file", map[string]any{"path": "f.go", "escape": true})
	viaInspect := mustRun(t, r, "inspect", map[string]any{"path": "f.go"})

	if !strings.Contains(viaRead.Text, "→") {
		t.Errorf("read_file {escape:true} не показал табы:\n%s", viaRead.Text)
	}
	if !strings.Contains(viaInspect.Text, "→") {
		t.Errorf("inspect не показал табы:\n%s", viaInspect.Text)
	}
	if !strings.Contains(viaRead.Text, "Точное содержимое") {
		t.Errorf("read_file {escape:true} не перешёл в escape-режим:\n%s", viaRead.Text)
	}
}

// TestReadFileEscapeCountsAsRead — inspect и read_file{escape} открывают файл
// для последующего edit_file, иначе пришлось бы читать его дважды.
func TestReadFileEscapeCountsAsRead(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.go")
	mustWrite(t, p, "старое")

	r := newTestReg(t, dir)
	if _, err := run(t, r, "read_file", map[string]any{"path": "f.go", "escape": true}); err != nil {
		t.Fatalf("read_file escape вернул ошибку: %v", err)
	}
	if !r.env.ReadFiles[p] {
		t.Errorf("файл не отмечен прочитанным: %v", r.env.ReadFiles)
	}
	if _, err := run(t, r, "edit_file", map[string]any{
		"path": "f.go", "old_string": "старое", "new_string": "новое",
	}); err != nil {
		t.Errorf("edit_file после escape-чтения вернул ошибку: %v", err)
	}
}

// TestReadFileWithoutEscapeIsPlain — обычное чтение не искажается маркерами.
func TestReadFileWithoutEscapeIsPlain(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "f.txt"), "обычный\tтекст\n")

	r := newTestReg(t, dir)
	res := mustRun(t, r, "read_file", map[string]any{"path": "f.txt"})
	if strings.Contains(res.Text, "→") {
		t.Errorf("обычное чтение подменило таб на маркер:\n%s", res.Text)
	}
}

// TestArgCoercion — числа и флаги принимаются не только из JSON.
//
// Аргументы собирают не только при разборе JSON: расширения, тесты и
// MCP-мост передают обычные int и bool. Раньше такой int молча заменялся
// значением по умолчанию, и инструмент работал не с теми offset/limit,
// которые ему передали, — без всякой ошибки.
func TestArgCoercion(t *testing.T) {
	for name, v := range map[string]any{
		"int":     7,
		"int64":   int64(7),
		"float64": float64(7.9),
		"json":    json.Number("7"),
	} {
		m := map[string]any{"n": v}
		if got := ArgInt(m, "n", -1); got != 7 {
			t.Errorf("ArgInt для %s = %d, ожидалось 7", name, got)
		}
	}
	// ArgFloat дробь не режет: 7.9 остаётся 7.9, иначе пороги вроде
	// «timeout > 0.5» срабатывают не там.
	if got := ArgFloat(map[string]any{"n": float64(7.9)}, "n", -1); got != 7.9 {
		t.Errorf("ArgFloat для 7.9 = %v", got)
	}
	if got := ArgFloat(map[string]any{"n": 3}, "n", -1); got != 3 {
		t.Errorf("ArgFloat для int = %v", got)
	}
	if got := ArgFloat(map[string]any{"n": json.Number("2.5")}, "n", -1); got != 2.5 {
		t.Errorf("ArgFloat для json.Number = %v", got)
	}
	// Отсутствующий и мусорный ключ — значение по умолчанию, без паники.
	if got := ArgInt(map[string]any{}, "нет", 5); got != 5 {
		t.Errorf("ArgInt для отсутствующего ключа = %d", got)
	}
	if got := ArgInt(map[string]any{"n": "мусор"}, "n", 5); got != 5 {
		t.Errorf("ArgInt для нечисловой строки = %d", got)
	}
	if got := ArgFloat(map[string]any{"n": "мусор"}, "n", 5); got != 5 {
		t.Errorf("ArgFloat для нечисловой строки = %v", got)
	}
	// Флаги приходят строками чаще, чем кажется: не все модели пишут JSON-тип.
	for _, s := range []string{"true", "да", "YES", "1"} {
		if !ArgBool(map[string]any{"b": s}, "b") {
			t.Errorf("ArgBool для %q = false", s)
		}
	}
	if ArgBool(map[string]any{"b": "false"}, "b") {
		t.Error("ArgBool для строки «false» = true")
	}
}
