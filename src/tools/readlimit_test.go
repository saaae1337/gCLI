package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLines — файл с заданным числом строк.
func writeLines(t *testing.T, p string, n int) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("строка ")
		b.WriteString(strings.Repeat("x", 3))
		b.WriteString("\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReadLinesFromOffset — offset обязан читать нужное место, а не
// «прочитал всё и разрезал»: на большом файле это разница между 4 МБ
// памяти и 4 ГБ.
func TestReadLinesFromOffset(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.txt")
	var b strings.Builder
	const total = 5000
	for i := 1; i <= total; i++ {
		if i == 1234 {
			b.WriteString("ИСКOMAЯ-СТРОКА\n")
			continue
		}
		b.WriteString("обычная строка\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	data, size, atEOF, err := readLinesFrom(p, 1200, 100, maxReadBytes)
	if err != nil {
		t.Fatal(err)
	}
	if size == 0 {
		t.Fatal("размер файла не определён")
	}
	if atEOF {
		t.Fatal("файл не закончился, отметка atEOF неверна")
	}
	if !strings.Contains(string(data), "ИСКOMAЯ-СТРОКА") {
		t.Fatal("нужная строка не попала в выборку начиная с offset=1201")
	}
	// Строк до offset попадать не должно: 100 строк начиная с 1201, значит
	// обычных строк в выборке ровно 99 (1201-я обычная, 1234-я — искомая).
	// Если бы skip игнорировался, обычных было бы 100 — и промах offset
	// «съедал» бы бюджет ответа, не показывая ничего нового.
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 100 {
		t.Fatalf("прочитано %d строк вместо 100", len(lines))
	}
	ordinary := 0
	for _, l := range lines {
		if l == "обычная строка" {
			ordinary++
		}
	}
	if ordinary != 99 {
		t.Fatalf("в выборке %d обычных строк вместо 99 — offset проигнорирован", ordinary)
	}
}

// TestReadLinesFromAtEOF — файл короче offset: агент должен получить честное
// «дальше ничего нет», а не пустую строку без объяснения.
func TestReadLinesFromAtEOF(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "small.txt")
	writeLines(t, p, 3)

	data, _, atEOF, err := readLinesFrom(p, 100, 10, maxReadBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !atEOF {
		t.Fatal("при skip за пределами файла atEOF обязан быть true")
	}
	if len(data) != 0 {
		t.Fatalf("данных быть не должно, получено %q", data)
	}
}

// TestReadLinesFromLimit — лимит строк соблюдается.
func TestReadLinesFromLimit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "many.txt")
	writeLines(t, p, 1000)

	data, _, atEOF, err := readLinesFrom(p, 0, 10, maxReadBytes)
	if err != nil {
		t.Fatal(err)
	}
	if atEOF {
		t.Fatal("файл из 1000 строк не мог закончиться после 10")
	}
	if n := len(strings.Split(strings.TrimRight(string(data), "\n"), "\n")); n != 10 {
		t.Fatalf("прочитано %d строк вместо 10", n)
	}
}

// TestReadFileLimited — потолок байт соблюдается и обрезка объявляется.
func TestReadFileLimited(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "fat.bin")
	// 64 КБ данных, потолок 1 КБ.
	if err := os.WriteFile(p, make([]byte, 64*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	data, size, truncated, err := readFileLimited(p, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Fatal("файл больше потолка — обрезка должна быть объявлена")
	}
	if len(data) != 1024 {
		t.Fatalf("прочитано %d байт вместо 1024", len(data))
	}
	if size != 64*1024 {
		t.Fatalf("размер файла определён неверно: %d", size)
	}
}

func TestReadFileLimitedSmallFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "small.txt")
	if err := os.WriteFile(p, []byte("привет"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, _, truncated, err := readFileLimited(p, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Fatal("маленький файл не должен считаться обрезанным")
	}
	if string(data) != "привет" {
		t.Fatalf("содержимое искажено: %q", data)
	}
}

func TestReadFileLimitedRejectsDir(t *testing.T) {
	dir := t.TempDir()
	if _, _, _, err := readFileLimited(dir, 1024); err == nil {
		t.Fatal("чтение каталога должно давать ошибку, а не пустой результат")
	}
}

// TestHReadFileLongLine — строка длиннее буфера (минифицированный js, base64)
// не должна ронять чтение и не должна съесть бюджет.
func TestHReadFileLongLine(t *testing.T) {
	work := t.TempDir()
	p := filepath.Join(work, "min.js")
	if err := os.WriteFile(p, []byte(strings.Repeat("a", 200*1024)+"\n"+"ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(Env{WorkDir: work})
	res, err := r.hReadFile(nil, map[string]any{"path": "min.js"})
	if err != nil {
		t.Fatalf("длинная строка не должна ронять чтение: %v", err)
	}
	if !strings.Contains(res.Text, "ok") {
		t.Fatal("хвост файла потерян")
	}
}
