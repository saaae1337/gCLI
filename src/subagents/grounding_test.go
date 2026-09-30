package subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------- Помощники ----------

func mkGrounding(t *testing.T) (*Grounding, string) {
	t.Helper()
	dir := t.TempDir()
	return NewGrounding(dir), dir
}

// writeFileOnDisk — создать файл с заданным числом строк.
func writeFileOnDisk(t *testing.T, dir, name string, lines int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		b.WriteString("line ")
		b.WriteString(itoa(i))
		b.WriteString("\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// claimKeys — ключи неподтверждённых утверждений для сравнения вердиктов.
func claimKeys(cs []Claim) string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Where)
	}
	return strings.Join(out, ",")
}

// readFull — результат чтения файла целиком (инструмент сообщает про EOF).
func readFull(path string, lastLine int) string {
	return "Файл: " + path + "\n\n1\tline 1\n" +
		"[файл закончился на строке " + itoa(lastLine) + ", показано строк: " + itoa(lastLine) + "]"
}

// readPart — результат частичного чтения с точным диапазоном.
func readPart(from, shown int) string {
	return "Файл: x.go\n\n" +
		"[показано строк: " + itoa(shown) + " начиная с " + itoa(from) + "; далее offset=" + itoa(from+shown) + "]"
}

// grepOut — результат grep: строки «путь:строка: текст».
func grepOut(hits ...string) string {
	return "Найдено " + itoa(len(hits)) + " совпадений в 1 файлах:\n" + strings.Join(hits, "\n")
}

// ---------- Сбор доказательств ----------

// TestObserveReadFull — чтение файла целиком подтверждает любую его строку.
func TestObserveReadFull(t *testing.T) {
	g, dir := mkGrounding(t)
	writeFileOnDisk(t, dir, "pool.go", 100)
	g.Observe("read_file", `{"path":"pool.go"}`, readFull("pool.go", 100), true)

	rep := g.Audit("Пул устроен в pool.go:42, аксессор в pool.go:99.")
	if !rep.Trustworthy {
		t.Fatalf("файл прочитан целиком, ссылки должны подтверждаться: %+v", rep.Unsupported)
	}
	if rep.Supported != 2 || rep.Checked != 2 {
		t.Fatalf("подтверждено %d из %d, ждали 2 из 2", rep.Supported, rep.Checked)
	}
	if rep.FilesRead != 1 {
		t.Fatalf("файлов прочитано %d, ждали 1", rep.FilesRead)
	}
}

// TestObserveReadRange — частичное чтение подтверждает только прочитанные строки.
//
// Главный сценарий против галлюцинаций: субагент прочитал кусок файла, а в
// отчёте сослался на строку за пределами этого куска. Такой ссылке верить нельзя.
func TestObserveReadRange(t *testing.T) {
	g, dir := mkGrounding(t)
	writeFileOnDisk(t, dir, "agent.go", 1000)
	g.Observe("read_file", `{"path":"agent.go","offset":120,"limit":40}`,
		readPart(120, 40), true)

	rep := g.Audit("Цикл в agent.go:130, а хвост в agent.go:900.")
	if rep.Supported != 1 {
		t.Fatalf("подтверждено %d, ждали 1 (только строка 130 в зоне 120-159)", rep.Supported)
	}
	if len(rep.Unsupported) != 1 {
		t.Fatalf("неподтверждённых %d, ждали 1: %+v", len(rep.Unsupported), rep.Unsupported)
	}
	if !strings.Contains(rep.Unsupported[0].Why, "120-159") {
		t.Fatalf("в причине нет зоны чтения: %q", rep.Unsupported[0].Why)
	}
	if rep.Trustworthy {
		t.Fatal("отчёт с неподтверждённой ссылкой не может быть trustworthy")
	}
}

// TestObserveGrepHits — grep подтверждает найденные строки без чтения файла.
func TestObserveGrepHits(t *testing.T) {
	g, dir := mkGrounding(t)
	writeFileOnDisk(t, dir, "pool.go", 300)
	g.Observe("grep", `{"pattern":"func Spawn"}`,
		grepOut("pool.go:195: func Spawn(", "pool.go:196: \t// doc"), true)

	rep := g.Audit("Spawn определён в pool.go:195.")
	if !rep.Trustworthy {
		t.Fatalf("строка найдена grep, ссылка должна подтверждаться: %+v", rep.Unsupported)
	}
	// Строка, которую grep не нашёл, не подтверждается.
	rep2 := g.Audit("Spawn определён в pool.go:197.")
	if rep2.Trustworthy {
		t.Fatal("строка 197 не найдена grep и не читалась — ссылка не подтверждена")
	}
}

// TestObserveMergedRanges — соседние чтения сливаются в один диапазон.
func TestObserveMergedRanges(t *testing.T) {
	g, dir := mkGrounding(t)
	writeFileOnDisk(t, dir, "big.go", 500)
	// Три чтения подряд: 1-10, 11-20, 30-40. Первые два — соседние.
	g.Observe("read_file", `{"path":"big.go","offset":1,"limit":10}`, readPart(1, 10), true)
	g.Observe("read_file", `{"path":"big.go","offset":11,"limit":10}`, readPart(11, 10), true)
	g.Observe("read_file", `{"path":"big.go","offset":30,"limit":11}`, readPart(30, 11), true)

	if !g.LineCovered("big.go", 10) || !g.LineCovered("big.go", 20) {
		t.Fatal("диапазон 1-20 должен слиться в один")
	}
	if g.LineCovered("big.go", 25) {
		t.Fatal("строка 25 между чтениями не покрыта")
	}
	if !g.LineCovered("big.go", 35) {
		t.Fatal("строка 35 в зоне 30-40 должна покрываться")
	}
	rep := g.Audit("Код в big.go:20 и big.go:35.")
	if !rep.Trustworthy {
		t.Fatalf("обе строки в зоне чтения: %+v", rep.Unsupported)
	}
}

// TestPhantomFile — файл, которого нет на диске, и который не читали.
func TestPhantomFile(t *testing.T) {
	g, _ := mkGrounding(t)
	g.Observe("read_file", `{"path":"real.go"}`, readFull("real.go", 10), true)

	rep := g.Audit("Ошибка в utils/helper.go:15, это критично.")
	if len(rep.Phantoms) != 1 || rep.Phantoms[0] != "utils/helper.go" {
		t.Fatalf("фантомы: %+v, ждали [utils/helper.go]", rep.Phantoms)
	}
	if rep.Trustworthy {
		t.Fatal("ссылка на несуществующий файл — отчёт не trustworthy")
	}
}

// TestPhantomCyrillicPath — фантом с нелатинским именем.
//
// Отдельный тест не для красоты: с ASCII-классами регулярка такого пути не
// находила вовсе, Phantoms оставался пустым, и выдуманный файл уходил наверх
// как подтверждённый факт. Тихая неудача проверки хуже явной ошибки.
func TestPhantomCyrillicPath(t *testing.T) {
	g, _ := mkGrounding(t)
	g.Observe("read_file", `{"path":"real.go"}`, readFull("real.go", 10), true)

	rep := g.Audit("Выдумка: ghost/нет-такого.go:5 и docs/Отчёт.md:2.")
	want := []string{"docs/Отчёт.md", "ghost/нет-такого.go"}
	if strings.Join(rep.Phantoms, ",") != strings.Join(want, ",") {
		t.Fatalf("фантомы: %+v, ждали %+v", rep.Phantoms, want)
	}
	if len(rep.Unsupported) != 2 {
		t.Fatalf("неподтверждённых ссылок %d, ждали 2: %+v", len(rep.Unsupported), rep.Unsupported)
	}
	if rep.Trustworthy {
		t.Fatal("фантомы с кириллицей должны ронять доверие")
	}
}

// TestCyrillicFileRead — ссылка на реально прочитанный файл с русским именем
// подтверждается: расширение проверки не должно ломать обратный случай.
func TestCyrillicFileRead(t *testing.T) {
	g, _ := mkGrounding(t)
	g.Observe("read_file", `{"path":"docs/Проверка.md"}`, readFull("docs/Проверка.md", 40), true)

	rep := g.Audit("Правило описано в docs/Проверка.md:12.")
	if !rep.Trustworthy || len(rep.Phantoms) != 0 {
		t.Fatalf("прочитанный файл с кириллицей должен подтверждаться: %+v", rep)
	}
	if rep.Supported != 1 {
		t.Fatalf("подтверждено %d ссылок, ждали 1", rep.Supported)
	}
}

// TestWebAddressesAreNotPaths — адреса и домены не считаются файлами проекта.
//
// Регулярка с Unicode-классами стала шире, и вместе с ней появился риск
// размечать `https://example.com/spec.md` как несуществующий файл. Для отчёта
// это шум, который в худшем случае отправляет главного агента искать несуществующий
// `https//example.com/spec.md`.
func TestWebAddressesAreNotPaths(t *testing.T) {
	g, dir := mkGrounding(t)
	// Каталог с точкой в имени неотличим от домена по виду, но на диске он есть.
	writeFileOnDisk(t, dir, "docs.go/main.go", 20)
	g.Observe("read_file", `{"path":"real.go"}`, readFull("real.go", 10), true)

	rep := g.Audit("См. https://example.com/spec.md:4 и docs.example.com/guide.html, " +
		"а также docs.go/main.go — впрочем, не читал его.")
	if len(rep.Phantoms) != 0 {
		t.Fatalf("адреса приняты за файлы проекта: %+v", rep.Phantoms)
	}
	// Настоящий файл с точкой в имени каталога — это файл, а не домен.
	if len(rep.Unread) != 1 || rep.Unread[0] != "docs.go/main.go" {
		t.Fatalf("непрочитанные: %+v, ждали [docs.go/main.go]", rep.Unread)
	}
}

// TestUnreadButExisting — существующий файл, который субагент не открывал.
//
// Это не галлюцинация, но и не проверенный факт: файл мог быть назван наугад.
func TestUnreadButExisting(t *testing.T) {
	g, dir := mkGrounding(t)
	writeFileOnDisk(t, dir, "other.go", 50)
	g.Observe("read_file", `{"path":"real.go"}`, readFull("real.go", 10), true)

	rep := g.Audit("Похоже, дело в other.go.")
	if len(rep.Phantoms) != 0 {
		t.Fatalf("файл существует, он не фантом: %+v", rep.Phantoms)
	}
	if len(rep.Unread) != 1 || rep.Unread[0] != "other.go" {
		t.Fatalf("непрочитанные: %+v, ждали [other.go]", rep.Unread)
	}
	// Непрочитанный файл не роняет доверие: вывод может быть осторожным.
	if !rep.Trustworthy {
		t.Fatal("непрочитанный файл не должен ронять Trustworthy")
	}
}

// TestPathFormsMatch — «pool.go» и «subagents/pool.go» это один файл.
func TestPathFormsMatch(t *testing.T) {
	g, dir := mkGrounding(t)
	writeFileOnDisk(t, dir, "pool.go", 100)
	g.Observe("read_file", `{"path":"subagents/pool.go"}`, readFull("subagents/pool.go", 100), true)

	rep := g.Audit("См. pool.go:50.")
	if !rep.Trustworthy {
		t.Fatalf("пути эквивалентны, ссылка должна подтвердиться: %+v", rep.Unsupported)
	}
}

// TestNoiseTokens — версии и сокращения не считаются ссылками на файлы.
func TestNoiseTokens(t *testing.T) {
	g, dir := mkGrounding(t)
	writeFileOnDisk(t, dir, "main.go", 10)
	g.Observe("read_file", `{"path":"main.go"}`, readFull("main.go", 10), true)

	rep := g.Audit("Версия v5.3.0 сборки 1.2.3, и т.д. и т.п. и т.к. Файл main.go:3.")
	if len(rep.Phantoms) != 0 || len(rep.Unread) != 0 {
		t.Fatalf("мусные токены приняты за ссылки: phantoms=%+v unread=%+v", rep.Phantoms, rep.Unread)
	}
}

// TestFailedToolIsNoEvidence — упавший инструмент не даёт доказательств.
func TestFailedToolIsNoEvidence(t *testing.T) {
	g, dir := mkGrounding(t)
	writeFileOnDisk(t, dir, "x.go", 100)
	g.Observe("read_file", `{"path":"x.go"}`, "", false)

	rep := g.Audit("Код в x.go:5.")
	if rep.Trustworthy {
		t.Fatal("чтение не удалось — ссылка не подтверждена")
	}
	if rep.FilesRead != 0 {
		t.Fatalf("упавшее чтение попало в файлы: %d", rep.FilesRead)
	}
}

// TestNoClaims — отчёт без ссылок не роняется и не шумит.
func TestNoClaims(t *testing.T) {
	g, _ := mkGrounding(t)
	rep := g.Audit("## Итог\n- Всё выглядит нормально.")
	if !rep.Trustworthy || rep.Checked != 0 {
		t.Fatalf("пустой отчёт должен быть ok: %+v", rep)
	}
	if rep.Score() != 1 {
		t.Fatalf("Score без проверяемых ссылок должен быть 1, получен %v", rep.Score())
	}
	if g.Report(rep) != "" {
		t.Fatal("блок проверки не должен появляться, если нечего проверять")
	}
}

// TestReportBlockMentionsUnsupported — в отчёт попадает разбор расхождений.
func TestReportBlockMentionsUnsupported(t *testing.T) {
	g, dir := mkGrounding(t)
	writeFileOnDisk(t, dir, "a.go", 500)
	g.Observe("read_file", `{"path":"a.go","offset":1,"limit":5}`, readPart(1, 5), true)

	block := g.Report(g.Audit("Факт в a.go:300."))
	if !strings.Contains(block, "a.go:300") {
		t.Fatalf("блок не содержит спорную ссылку:\n%s", block)
	}
	if !strings.Contains(block, "не доверяй") && !strings.Contains(block, "НЕ подтверждено") {
		t.Fatalf("в блоке нет предупреждения:\n%s", block)
	}
}

// TestObserveCaps — потолки журнала не ломают работу при потоке вызовов.
func TestObserveCaps(t *testing.T) {
	g := &Grounding{files: map[string]*fileRec{}, maxFiles: 5, maxItems: 20, maxHits: 30}
	for i := 0; i < 100; i++ {
		g.Observe("read_file", `{"path":"f`+itoa(i)+`.go"}`, readFull("f.go", 10), true)
	}
	if len(g.items) > 20 {
		t.Fatalf("предметов %d, потолок 20", len(g.items))
	}
	if len(g.files) > 5 {
		t.Fatalf("файлов %d, потолок 5", len(g.files))
	}
	// Заземление остаётся рабочим.
	if rep := g.Audit("Ссылка на f1.go:2."); len(rep.Unsupported) > 1 {
		t.Fatalf("сбой проверки после потолков: %+v", rep.Unsupported)
	}
}

// TestDeterministicVerdict — вердикт не зависит от порядка обхода map.
func TestDeterministicVerdict(t *testing.T) {
	g, dir := mkGrounding(t)
	for _, n := range []string{"a.go", "b.go", "c.go", "d.go", "e.go"} {
		writeFileOnDisk(t, dir, n, 50)
		g.Observe("read_file", `{"path":"`+n+`"}`, readFull(n, 50), true)
	}
	report := "Факты: a.go:10, b.go:20, c.go:30, zz.go:5, d.go:40, q.go:1."
	first := g.Audit(report)
	for i := 0; i < 20; i++ {
		got := g.Audit(report)
		if strings.Join(got.Phantoms, ",") != strings.Join(first.Phantoms, ",") {
			t.Fatalf("фантомы нестабильны: %+v против %+v", got.Phantoms, first.Phantoms)
		}
		if claimKeys(got.Unsupported) != claimKeys(first.Unsupported) {
			t.Fatalf("неподтверждённые нестабильны: %+v против %+v", got.Unsupported, first.Unsupported)
		}
	}
	if len(first.Phantoms) != 2 {
		t.Fatalf("фантомы %+v, ждали zz.go и q.go", first.Phantoms)
	}
}

// TestMultiReadPaths — multi_read засчитывает все файлы пачки.
func TestMultiReadPaths(t *testing.T) {
	g, dir := mkGrounding(t)
	writeFileOnDisk(t, dir, "one.go", 10)
	writeFileOnDisk(t, dir, "two.go", 10)
	g.Observe("multi_read", `{"paths":["one.go","two.go"]}`, readFull("x", 10), true)

	rep := g.Audit("Один в one.go:1, другой в two.go:2.")
	if !rep.Trustworthy {
		t.Fatalf("оба файла прочитаны пачкой: %+v", rep.Unsupported)
	}
	if rep.FilesRead != 2 {
		t.Fatalf("файлов %d, ждали 2", rep.FilesRead)
	}
}

// TestFileEvidenceWhenNoPaths — вызов без путей не создаёт мусор.
func TestFileEvidenceWhenNoPaths(t *testing.T) {
	g, _ := mkGrounding(t)
	g.Observe("read_file", `{}`, readFull("x.go", 10), true)
	if len(g.Files()) != 0 {
		t.Fatalf("мусорные файлы: %+v", g.Files())
	}
}

// TestNilGroundingSafe — методы переживают nil: так безопаснее в коде вызова.
func TestNilGroundingSafe(t *testing.T) {
	var g *Grounding
	g.Observe("read_file", `{"path":"x.go"}`, readFull("x.go", 10), true)
	if rep := g.Audit("x.go:1"); !rep.Trustworthy {
		t.Fatal("nil-журнал должен давать нейтральный вердикт")
	}
	if g.Files() != nil {
		t.Fatal("nil-журнал не должен возвращать файлы")
	}
	if g.Report(nil) != "" {
		t.Fatal("nil-отчёт не должен давать текст")
	}
}

// TestExecAndWebEvidence — команды и страницы фиксируются как доказательства.
func TestExecAndWebEvidence(t *testing.T) {
	g, _ := mkGrounding(t)
	g.Observe("bash", `{"command":"go test ./..."}`, "ok  gcli  1.2s", true)
	g.Observe("web_fetch", `{"url":"https://go.dev/doc/"}`, "страница", true)

	if len(g.items) != 2 {
		t.Fatalf("доказательств %d, ждали 2", len(g.items))
	}
	if g.items[0].Kind != EvExec || g.items[0].Cmd != "go test ./..." {
		t.Fatalf("команда не записана: %+v", g.items[0])
	}
	if g.items[1].Kind != EvWeb || g.items[1].URL != "https://go.dev/doc/" {
		t.Fatalf("URL не записан: %+v", g.items[1])
	}
	// Команда не подтверждает строки кода: go test не читает исходники.
	if rep := g.Audit("Сборка падает в main.go:20."); rep.Trustworthy {
		t.Fatal("команда не должна подтверждать строки исходника")
	}
}
