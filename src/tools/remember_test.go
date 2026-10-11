package tools

import (
	"os"
	"strings"
	"testing"
)

// ---------- долговременная память ----------

// memHome — подменить каталог данных и вернуть путь к файлу памяти.
func memHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GCLI_HOME", dir)
	return MemoryFile()
}

func TestMemoryRoundTrip(t *testing.T) {
	path := memHome(t)
	if _, err := AddMemory("сборка: ./build.sh one", "быстро"); err != nil {
		t.Fatal(err)
	}
	entries := ReadMemory()
	if len(entries) != 1 {
		t.Fatalf("записей %d, ждали 1", len(entries))
	}
	if entries[0].Fact != "сборка: ./build.sh one" || entries[0].Note != "быстро" {
		t.Errorf("запись разобрана неверно: %+v", entries[0])
	}
	if entries[0].Date == "" {
		t.Error("нет даты")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("файл памяти не создан: %v", err)
	}
}

// TestMemoryDeduplicates — тот же факт повторно не плодится, даже если
// отличается регистром, пробелами и точкой в конце.
func TestMemoryDeduplicates(t *testing.T) {
	memHome(t)
	if _, err := AddMemory("тест на дедупликацию", "раз"); err != nil {
		t.Fatal(err)
	}
	msg, err := AddMemory("  Тест на дедупликацию.  ", "два")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "уже был записан") {
		t.Errorf("повтор не опознан как повтор: %s", msg)
	}
	if got := len(ReadMemory()); got != 1 {
		t.Errorf("записей %d после повтора, ждали 1", got)
	}
}

// TestRememberSameFactUpdatesNote — повторное запоминание того же факта
// с новым комментарием обязано обновлять комментарий и поднимать
// запись вверх.
//
// Комментарий в коде так и обещал, но тело делало writeMemory с
// нетронутым списком: и комментарий оставался прежним, и запись не
// двигалась. Для памяти это не мелочь — записи, однажды ушедшие в
// хвост, сворачиваются в архив, и факт, который агент только что
// подтвердил как важный, мог исчезнуть из выжимки для промпта.
func TestRememberSameFactUpdatesNote(t *testing.T) {
	memHome(t)
	if _, err := AddMemory("тест на обновление", "старая пометка"); err != nil {
		t.Fatal(err)
	}
	// Между записями появляется более свежий факт — он должен остаться
	// первым, а обновлённый факт встать сразу за ним.
	if _, err := AddMemory("другой факт", "свежий"); err != nil {
		t.Fatal(err)
	}
	if _, err := AddMemory("Тест на обновление", "новая пометка"); err != nil {
		t.Fatal(err)
	}

	entries := ReadMemory()
	if len(entries) != 2 {
		t.Fatalf("записей %d, ждали 2 (повтор не должен плодить): %+v", len(entries), entries)
	}
	// Подтверждённый только что факт становится самым свежим — тем же
	// порядком, что и при обычной записи.
	if entries[0].Fact != "Тест на обновление" {
		t.Fatalf("обновлённый факт не поднят вверх: %+v", entries)
	}
	if entries[0].Note != "новая пометка" {
		t.Errorf("комментарий не обновился: %q", entries[0].Note)
	}
	if entries[1].Fact != "другой факт" {
		t.Errorf("остальные записи потеряны или переставлены: %+v", entries[1:])
	}
}

// TestRememberMovesToFrontWhenAlone — обновление единственной записи
// обязано её обновить, даже когда других нет.
func TestRememberMovesToFrontWhenAlone(t *testing.T) {
	memHome(t)
	if _, err := AddMemory("единственный факт", "было"); err != nil {
		t.Fatal(err)
	}
	if _, err := AddMemory("единственный факт", "стало"); err != nil {
		t.Fatal(err)
	}
	entries := ReadMemory()
	if len(entries) != 1 {
		t.Fatalf("записей %d, ждали 1", len(entries))
	}
	if entries[0].Note != "стало" {
		t.Errorf("комментарий не обновился: %q", entries[0].Note)
	}
}

func TestMemoryRejectsEmptyFact(t *testing.T) {
	memHome(t)
	if _, err := AddMemory("   ", ""); err == nil {
		t.Error("пустой факт принят")
	}
}

// TestForgetMemoryBySubstring — forget убирает по подстроке и не
// трогает остальное.
func TestForgetMemoryBySubstring(t *testing.T) {
	memHome(t)
	for _, f := range []string{"сборка: ./build.sh", "тесты: go test ./...", "десктоп на electron"} {
		if _, err := AddMemory(f, ""); err != nil {
			t.Fatal(err)
		}
	}
	n, err := ForgetMemory("build.sh")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("убрано %d, ждали 1", n)
	}
	if got := len(ReadMemory()); got != 2 {
		t.Errorf("осталось %d записей", got)
	}
	n, err = ForgetMemory("такого нет")
	if err != nil || n != 0 {
		t.Errorf("пустой поиск: n=%d err=%v", n, err)
	}
	if _, err := ForgetMemory("  "); err == nil {
		t.Error("пустой запрос принят")
	}
}

// TestMemoryArchiveKeepsEntries — старые записи сворачиваются, но не
// исчезают: знание не должно пропадать бесследно.
func TestMemoryArchiveKeepsEntries(t *testing.T) {
	path := memHome(t)
	for i := 0; i < maxMemoryLines+10; i++ {
		if _, err := AddMemory("факт "+string(rune('a'+i%26))+itoaMem(i), ""); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Архив") {
		t.Error("раздел архива не появился")
	}
	// Все записи на месте: и активные, и свёрнутые.
	if got := len(ParseMemory(string(raw))); got != maxMemoryLines+10 {
		t.Errorf("в файле %d записей, ждали %d — архив выбрасывает знание",
			got, maxMemoryLines+10)
	}
}

// TestMemoryDigestIncludesPinnedAndFresh — в выжимку идут и помеченные
// записи, и свежие, и она остаётся в оговорённых границах.
func TestMemoryDigestIncludesPinnedAndFresh(t *testing.T) {
	memHome(t)
	for i := 0; i < 40; i++ {
		note := ""
		if i%3 == 0 {
			note = "важно"
		}
		if _, err := AddMemory("факт "+itoaMem(i), note); err != nil {
			t.Fatal(err)
		}
	}
	d := MemoryDigest(24)
	if !strings.HasPrefix(d, memoryBlockStart) || !strings.HasSuffix(d, memoryBlockEnd) {
		t.Errorf("выжимка без машинных маркеров:\n%s", d)
	}
	if !strings.Contains(d, "важно") {
		t.Error("помеченные записи в выжимку не попали")
	}
	if n := strings.Count(d, "\n- "); n > 24 {
		t.Errorf("в выжимке %d записей при лимите 24", n)
	}
	if MemoryDigest(24) != d {
		t.Error("выжимка нестабильна между вызовами")
	}
}

func TestMemoryDigestEmpty(t *testing.T) {
	memHome(t)
	if got := MemoryDigest(24); got != "" {
		t.Errorf("пустая память дала выжимку: %q", got)
	}
}

// TestParseMemoryIgnoresGarbage — чужой текст в файле памяти не должен
// превращаться в записи.
func TestParseMemoryIgnoresGarbage(t *testing.T) {
	got := ParseMemory("# Память\nпросто текст\n- не дата факт\n- 2026-09-29 нормальный факт\n")
	if len(got) != 1 {
		t.Fatalf("разобрано %d записей: %+v", len(got), got)
	}
	if got[0].Fact != "нормальный факт" || got[0].Note != "" {
		t.Errorf("запись разобрана неверно: %+v", got[0])
	}
}

func itoaMem(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
