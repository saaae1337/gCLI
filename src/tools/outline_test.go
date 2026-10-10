package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile — файл с заданным содержимым во временном каталоге.
func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func outlineOf(t *testing.T, r *Registry, args map[string]any) Result {
	t.Helper()
	res, err := r.hOutline(context.Background(), args)
	if err != nil {
		t.Fatalf("outline: %v", err)
	}
	return res
}

const goSample = `package main

import "fmt"

// Widget — то, что показывают.
type Widget struct {
	Name string
	Size int
}

// Render — нарисовать виджет.
func (w *Widget) Render(theme string, dark bool) error {
	fmt.Println(w.Name)
	return nil
}

// Build — собрать виджет по имени.
func Build(name string, opts ...string) (*Widget, error) {
	return &Widget{Name: name}, nil
}

func main() {
	fmt.Println("ok")
}
`

func TestOutlineGoExact(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "widget.go", goSample)
	r := New(Env{WorkDir: dir})
	got := outlineOf(t, r, map[string]any{"path": p}).Text

	for _, want := range []string{
		"type Widget struct",
		"func (w *Widget) Render(theme string, dark bool) error",
		"func Build(name string, opts ...string) (*Widget, error)",
		"func main()",
		"точный разбор",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("нет %q в выводе:\n%s", want, got)
		}
	}
	if strings.Contains(got, ")(error)") {
		t.Errorf("результат без скобок должен идти через пробел, а не вплотную:\n%s", got)
	}
	if strings.Contains(got, "func (*Widget)") {
		t.Errorf("получатель потерял имя переменной:\n%s", got)
	}
}

// TestOutlineGoLineNumbersAreReal — номера строк обязаны совпадать с
// файлом. По ним агент открывает место через read_file с offset, и
// ошибка на единицу уводит его в соседнее объявление.
func TestOutlineGoLineNumbersAreReal(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "widget.go", goSample)
	lines := strings.Split(goSample, "\n")
	r := New(Env{WorkDir: dir})
	got := outlineOf(t, r, map[string]any{"path": p}).Text

	for _, decl := range []struct {
		name string
		line int
	}{{"Widget", 6}, {"Render", 12}, {"Build", 18}, {"main", 22}} {
		want := strings.TrimSpace(lines[decl.line-1])
		found := false
		for _, l := range strings.Split(got, "\n") {
			cols := strings.SplitN(strings.TrimSpace(l), "│", 2)
			if len(cols) != 2 || strings.TrimSpace(cols[0]) != itoa(decl.line) {
				continue
			}
			found = true
			// Сигнатура обрезает тело и дописывает описание, поэтому
			// сверяем, что строка из файла — начало выведенной.
			got := strings.TrimSpace(cols[1])
			if !strings.HasPrefix(got, strings.TrimSuffix(want, " {")) {
				t.Errorf("строка %d: в файле %q, в выводе %q", decl.line, want, got)
			}
		}
		if !found {
			t.Errorf("объявление %s на строке %d не попало в вывод:\n%s", decl.name, decl.line, got)
		}
	}
}

func itoa(n int) string {
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

// TestOutlineGoDocFirstLine — описание подтягивается: по нему видно, что
// функция делает, не открывая тело.
func TestOutlineGoDocFirstLine(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "widget.go", goSample)
	r := New(Env{WorkDir: dir})
	got := outlineOf(t, r, map[string]any{"path": p}).Text
	for _, want := range []string{"то, что показывают", "нарисовать виджет", "собрать виджет по имени"} {
		if !strings.Contains(got, want) {
			t.Errorf("нет описания %q:\n%s", want, got)
		}
	}
}

// TestOutlineGoKeepsUnexported — внутренние функции тоже объявления:
// агент правит их не реже экспортируемых.
func TestOutlineGoKeepsUnexported(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "widget.go", goSample)
	r := New(Env{WorkDir: dir})
	if got := outlineOf(t, r, map[string]any{"path": p}).Text; !strings.Contains(got, "func main()") {
		t.Errorf("неэкспортированная функция пропущена:\n%s", got)
	}
}

// TestOutlineBrokenGoStillAnswers — файл читают посреди правки, и
// синтаксис в этот момент часто негодный. Отказ здесь означал бы, что
// агент вообще не сможет заглянуть в файл, который сам же и испортил.
func TestOutlineBrokenGoStillAnswers(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "broken.go", "package main\n\nfunc ok() {}\n\nfunc bad( {\n")
	r := New(Env{WorkDir: dir})
	got := outlineOf(t, r, map[string]any{"path": p}).Text
	if !strings.Contains(got, "func ok()") {
		t.Errorf("разбор битого файла потерял то, что разобрать удалось:\n%s", got)
	}
}

// TestOutlineFilter — фильтр оставляет нужное и не тащит остальное.
func TestOutlineFilter(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "widget.go", goSample)
	r := New(Env{WorkDir: dir})
	got := outlineOf(t, r, map[string]any{"path": p, "filter": "Render"}).Text
	if !strings.Contains(got, "Render") {
		t.Fatalf("фильтр убрал нужное:\n%s", got)
	}
	if strings.Contains(got, "func main()") {
		t.Errorf("фильтр оставил лишнее:\n%s", got)
	}
}

func TestOutlineHeuristicLanguages(t *testing.T) {
	cases := []struct {
		name string
		file string
		body string
		want []string
		skip []string
	}{
		{
			name: "python",
			file: "app.py",
			body: "# Загрузчик виджетов.\nclass Loader:\n    pass\n\nasync def load(path):\n    return 1\n\ndef helper():\n    pass\n",
			want: []string{"class Loader", "async def load", "def helper", "Загрузчик виджетов"},
		},
		{
			name: "typescript",
			file: "app.ts",
			body: "// Точка входа.\nexport function boot(): void {}\nexport class Widget {}\nexport interface Opts {}\nexport const make = (a: number) => a;\n",
			want: []string{"boot", "class Widget", "Opts", "make", "Точка входа"},
		},
		{
			name: "rust",
			file: "lib.rs",
			body: "/// Структура виджета.\npub struct Widget {}\npub fn render(w: &Widget) {}\nimpl Widget {}\n",
			want: []string{"struct Widget", "fn render", "impl", "Структура виджета"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p := writeFile(t, dir, c.file, c.body)
			r := New(Env{WorkDir: dir})
			got := outlineOf(t, r, map[string]any{"path": p}).Text
			if !strings.Contains(got, "эвристика") {
				t.Errorf("не сказано, что разбор эвристический:\n%s", got)
			}
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("нет %q:\n%s", w, got)
				}
			}
			for _, s := range c.skip {
				if strings.Contains(got, s) {
					t.Errorf("лишнее %q:\n%s", s, got)
				}
			}
		})
	}
}

// TestOutlineHeuristicSkipsCalls — эвристика не должна выдавать вызовы за
// объявления: список, наполовину состоящий из мусора, хуже пустого.
func TestOutlineHeuristicSkipsCalls(t *testing.T) {
	dir := t.TempDir()
	body := "import os\n\ndef run():\n    helper()\n    return load('x')\n\ndef helper():\n    pass\n"
	p := writeFile(t, dir, "a.py", body)
	r := New(Env{WorkDir: dir})
	got := outlineOf(t, r, map[string]any{"path": p}).Text
	if strings.Count(got, "def ") != 2 {
		t.Errorf("ожидались два объявления, вызовы попали в список:\n%s", got)
	}
}

// TestOutlineUnsupportedLanguageHonest — незнакомый язык должен сказать
// об этом прямо, а не вернуть пустой список, который читается как
// «файл пустой».
func TestOutlineUnsupportedLanguageHonest(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "main.c", "int main(void) { return 0; }\n")
	r := New(Env{WorkDir: dir})
	got := outlineOf(t, r, map[string]any{"path": p}).Text
	if !strings.Contains(got, "не разбирается") {
		t.Errorf("не сказано, что язык не поддержан:\n%s", got)
	}
	if strings.Contains(got, "объявлений: 0") {
		t.Errorf("пустой список выглядит как «файл пустой»:\n%s", got)
	}
}

// TestOutlineTruncationAnnounced — молчаливое усечение читается как
// «объявлений больше нет».
func TestOutlineTruncationAnnounced(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("package main\n\n")
	for i := 0; i < outlineMaxSymbols+40; i++ {
		b.WriteString("func f")
		b.WriteString(itoa(i))
		b.WriteString("() {}\n")
	}
	p := writeFile(t, dir, "many.go", b.String())
	r := New(Env{WorkDir: dir})
	got := outlineOf(t, r, map[string]any{"path": p}).Text
	if !strings.Contains(got, "сузь список через filter") {
		t.Errorf("усечение не объявлено:\n%s", got[len(got)-300:])
	}
}

// TestOutlineRespectsSandbox — инструмент читает файл и обязан идти
// через pathArg, иначе песочница обходится тем же, чем обходилась в
// 6.2.1 через verify.
func TestOutlineRespectsSandbox(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.go")
	if err := os.WriteFile(outside, []byte(goSample), 0o644); err != nil {
		t.Fatal(err)
	}
	sb := NewSandbox(dir)
	r := New(Env{WorkDir: dir, Sandbox: sb})
	if _, err := r.hOutline(context.Background(), map[string]any{"path": outside}); err == nil {
		t.Fatal("песочница пропустила файл за пределами рабочего каталога")
	}
}

// TestOutlineDoesNotMarkRead — outline даёт карту, а не содержимое.
// Пометив файл прочитанным, инструмент разрешил бы edit_file без
// реального чтения, и подтверждённая правка работала бы вслепую.
func TestOutlineDoesNotMarkRead(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "widget.go", goSample)
	read := map[string]bool{}
	r := New(Env{WorkDir: dir, ReadFiles: read})
	outlineOf(t, r, map[string]any{"path": p})
	if len(read) != 0 {
		t.Errorf("outline пометил файл прочитанным: %v", read)
	}
}

// TestOutlineBeatsReadingWholeFile — весь смысл инструмента в том, чтобы
// не читать файл целиком. Меряется против настоящего read_file на файле
// реального размера: на игрушечных двадцати строках выигрыш был бы
// показательным, а на большом файле его нет вовсе.
func TestOutlineBeatsReadingWholeFile(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("package big\n\n")
	for i := 0; i < 120; i++ {
		fmt.Fprintf(&b, `// Handler%03d — обработать запрос %d по правилам %d.
//
// Возвращает ответ и код, который клиент покажет пользователю.
func Handle%03d(w http.ResponseWriter, r *http.Request) error {
	if r == nil {
		return fmt.Errorf("nil request %%d", %d)
	}
	for i := 0; i < 10; i++ {
		fmt.Println("строка тела %%d", i)
	}
	return nil
}

`, i, i, i, i, i)
	}
	p := writeFile(t, dir, "big.go", b.String())
	r := New(Env{WorkDir: dir})

	out := outlineOf(t, r, map[string]any{"path": p})
	read, err := r.hReadFile(context.Background(), map[string]any{"path": p})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Text) >= len(read.Text) {
		t.Errorf("карта %d символов, чтение %d — инструмент не экономит ничего:\n%s",
			len(out.Text), len(read.Text), out.Text[:min(400, len(out.Text))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
