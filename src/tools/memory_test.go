package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gcli/core"
)

// ---------- память проекта ----------

// memManager — менеджер памяти с отдельным проектом и глобальным
// каталогом, чтобы не смешивать их.
func memManager(t *testing.T) (*Memory, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GCLI_HOME", home)
	work := t.TempDir()
	return NewMemory(work, core.NewStore()), work, filepath.Join(home, "GCLI.md")
}

// TestMemoryFindsProjectFileByPriority — при нескольких файлах памяти
// берётся первый по приоритету, а не все сразу: иначе одна и та же
// инструкция попадала бы в промпт дважды.
func TestMemoryFindsProjectFileByPriority(t *testing.T) {
	m, work, _ := memManager(t)
	if err := os.WriteFile(filepath.Join(work, "CLAUDE.md"), []byte("из CLAUDE"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := m.Files()
	if len(files) != 1 {
		t.Fatalf("найдено файлов %d, ждали 1: %v", len(files), files)
	}
	if filepath.Base(files[0]) != "CLAUDE.md" {
		t.Errorf("приоритет нарушен: %s", files[0])
	}

	if err := os.WriteFile(filepath.Join(work, "GCLI.md"), []byte("из GCLI"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(m.Files()[0]); got != "GCLI.md" {
		t.Errorf("GCLI.md должен быть приоритетнее: %s", got)
	}
}

// TestMemorySkipsDirectories — каталог с именем GCLI.md памятью не
// считается: иначе в промпт уехал бы отказ читать вместо содержимого.
func TestMemorySkipsDirectories(t *testing.T) {
	m, work, _ := memManager(t)
	if err := os.MkdirAll(filepath.Join(work, "GCLI.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := len(m.Files()); got != 0 {
		t.Errorf("каталог принят за файл памяти: %d", got)
	}
}

// TestMemoryCollectLabelsSources — проектная и глобальная памяти
// подписываются по-разному: иначе агент не понимает, что из этого
// верно для репозитория, а что — для машины.
func TestMemoryCollectLabelsSources(t *testing.T) {
	m, work, global := memManager(t)
	if err := os.WriteFile(filepath.Join(work, "GCLI.md"), []byte("проектные факты"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(global), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(global, []byte("глобальные привычки"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := m.Collect()
	if !strings.Contains(got, "проектные факты") || !strings.Contains(got, "глобальные привычки") {
		t.Fatalf("не собрано всё:\n%s", got)
	}
	if !strings.Contains(got, "Память проекта") || !strings.Contains(got, "Память глобальная") {
		t.Errorf("источники не подписаны:\n%s", got)
	}
}

func TestMemoryCollectEmpty(t *testing.T) {
	m, _, _ := memManager(t)
	if got := m.Collect(); got != "" {
		t.Errorf("без файлов памяти вернулось %q", got)
	}
}

// TestMemoryCollectTruncatesBigFile — огромный файл памяти не должен
// вытеснять задачу из промпта.
//
// Потолок задан в символах, а не в байтах: кириллица даёт вдвое
// больше байт на тот же символ, так что в байтах выходит около 47 КБ
// вместо 24. Это рассогласование единиц, а не поломка — ограничение
// действует, и именно символы верно переводятся в токены. Тест
// проверяет, что вывод остаётся ограниченным и много меньше
// источника, а не сверяет байты с числом в коде.
func TestMemoryCollectTruncatesBigFile(t *testing.T) {
	m, work, _ := memManager(t)
	const oneWord = "подробности "
	big := strings.Repeat(oneWord, 8000)
	if err := os.WriteFile(filepath.Join(work, "GCLI.md"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	got := m.Collect()
	if runes := len([]rune(got)); runes > 24*1024+1024 {
		t.Errorf("память проекта не урезана: %d символов", runes)
	}
	if len(got) >= len(big)/2 {
		t.Errorf("усечения нет: вывод %d при источнике %d байт", len(got), len(big))
	}
	if !strings.Contains(got, "обрезано") {
		t.Errorf("усечение не объявлено — агент примет короткую память за полную:\n%s",
			got[:min2(len(got), 400)])
	}
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestMemoryProjectFilePrefersExisting — ProjectFile возвращает
// существующий файл, а если его нет — тот, который будет создан.
func TestMemoryProjectFilePrefersExisting(t *testing.T) {
	m, work, _ := memManager(t)
	if got := filepath.Base(m.ProjectFile()); got != "GCLI.md" {
		t.Errorf("для нового проекта ждали GCLI.md, получили %s", got)
	}
	if err := os.WriteFile(filepath.Join(work, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(m.ProjectFile()); got != "AGENTS.md" {
		t.Errorf("существующий файл не найден: %s", got)
	}
}

// TestMemoryAppendFactCreatesTemplate — первый факт создаёт файл по
// шаблону, и шаблон остаётся, чтобы человек видел, что заполнять.
func TestMemoryAppendFactCreatesTemplate(t *testing.T) {
	m, work, _ := memManager(t)
	path, err := m.AppendFact("сборка: ./build.sh one")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "GCLI.md" {
		t.Errorf("создан не тот файл: %s", path)
	}
	raw, err := os.ReadFile(filepath.Join(work, "GCLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, "## Команды") {
		t.Errorf("шаблон не записан:\n%s", body)
	}
	if !strings.Contains(body, "- сборка: ./build.sh one") {
		t.Errorf("факт не дописан:\n%s", body)
	}
}

// TestMemoryAppendFactKeepsExisting — факты накапливаются, а не
// затирают друг друга.
func TestMemoryAppendFactKeepsExisting(t *testing.T) {
	m, _, _ := memManager(t)
	for _, f := range []string{"первый факт", "второй факт", "третий факт"} {
		if _, err := m.AppendFact(f); err != nil {
			t.Fatal(err)
		}
	}
	got := m.Collect()
	for _, f := range []string{"первый факт", "второй факт", "третий факт"} {
		if !strings.Contains(got, f) {
			t.Errorf("факт %q потерян:\n%s", f, got)
		}
	}
}

// TestMemoryAppendFactAppendsToExisting — факт идёт в существующий
// файл, а не перезаписывает его.
func TestMemoryAppendFactAppendsToExisting(t *testing.T) {
	m, work, _ := memManager(t)
	if err := os.WriteFile(filepath.Join(work, "AGENTS.md"), []byte("мои правила\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err := m.AppendFact("новый факт")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "AGENTS.md" {
		t.Errorf("факт пошёл не в тот файл: %s", path)
	}
	raw, _ := os.ReadFile(filepath.Join(work, "AGENTS.md"))
	if !strings.Contains(string(raw), "мои правила") {
		t.Errorf("прежнее содержимое затёрто:\n%s", raw)
	}
}

// ---------- @упоминания ----------

func TestApplyMentionsAttachesFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("содержимое файла"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ApplyMentions(dir, "посмотри @a.txt пожалуйста")
	if !strings.Contains(got, "содержимое файла") {
		t.Errorf("файл не приложен:\n%s", got)
	}
	if strings.Contains(got, "@a.txt ") && !strings.Contains(got, "содержимое") {
		t.Errorf("ссылка не раскрыта: %s", got)
	}
}

func TestApplyMentionsExplainsProblems(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "каталог"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ApplyMentions(dir, "открой @нет-такого.txt"); !strings.Contains(got, "не найден") {
		t.Errorf("отсутствующий файл не назван: %s", got)
	}
	if got := ApplyMentions(dir, "открой @каталог"); !strings.Contains(got, "каталог") {
		t.Errorf("каталог не распознан: %s", got)
	}
}

// TestApplyMentionsSkipsTrailingPunctuation — «посмотри @a.txt.» должно
// приложить файл, а не искать файл с точкой в имени.
func TestApplyMentionsSkipsTrailingPunctuation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("есть"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ApplyMentions(dir, "посмотри @a.txt.")
	if !strings.Contains(got, "есть") {
		t.Errorf("точка в конце не отброшена: %s", got)
	}
}

// TestApplyMentionsRespectsLimit — больше восьми приложений быть не
// должно: иначе один запрос съедает весь контекст.
func TestApplyMentionsRespectsLimit(t *testing.T) {
	dir := t.TempDir()
	var text strings.Builder
	for i := 0; i < 12; i++ {
		name := "f" + itoaMem(i) + ".txt"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("содержимое "+itoaMem(i)), 0o644); err != nil {
			t.Fatal(err)
		}
		text.WriteString(" @" + name)
	}
	got := ApplyMentions(dir, text.String())
	if n := strings.Count(got, "содержимое"); n > 8 {
		t.Errorf("приложено %d файлов при лимите 8", n)
	}
	if !strings.Contains(got, "лимит") {
		t.Errorf("лимит не объявлен:\n%s", got)
	}
}

// TestApplyMentionsKeepsPlainText — обычный текст без @ не трогается.
func TestApplyMentionsKeepsPlainText(t *testing.T) {
	const s = "просто текст без упоминаний и почты a@b.c"
	if got := ApplyMentions(t.TempDir(), s); got != s {
		t.Errorf("текст изменён: %q", got)
	}
}
