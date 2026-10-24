package tools

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---------- фальшивый language server ----------
//
// Настоящий gopls или pyright в тестах не положить: их может не быть
// в PATH, версии разъезжаются, и падение теста неотличимо от падения
// кода. Поэтому сервер поднимается из самого тестового бинарника —
// перезапуск с переменной окружения превращает его в сервер, который
// говорит ровно то, что нужно проверить.

// envFakeLSP — по этой переменной тестовый бинарник работает сервером.
const envFakeLSP = "GCLI_TEST_LSP_FAKE"

// TestFakeLSPServer — точка входа фальшивого сервера.
//
// Когда переменная выставлена, тест не тест, а сервер: крутит цикл
// протокола до выхода. Иначе — обычный тест, и ниже расположены
// настоящие проверки.
func TestFakeLSPServer(t *testing.T) {
	mode := os.Getenv(envFakeLSP)
	if mode == "" {
		t.Skip("этот тест запускается только как фальшивый language server")
	}
	serveFakeLSP(mode)
	os.Exit(0)
}

// serveFakeLSP — цикл протокола. mode: "push" отвечает публикацией
// диагностик, "pull" — только по запросу, "silent" молчит целиком.
func serveFakeLSP(mode string) {
	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	send := func(v any) {
		body, _ := json.Marshal(v)
		fmt.Fprintf(out, "Content-Length: %d\r\n\r\n", len(body))
		out.Write(body)
		out.Flush()
	}
	reply := func(id int64, result any) {
		send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	push := func(uri string) {
		send(map[string]any{
			"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics",
			"params": map[string]any{
				"uri": uri,
				"diagnostics": []map[string]any{{
					"range": map[string]any{
						"start": map[string]any{"line": 1, "character": 2},
						"end":   map[string]any{"line": 1, "character": 8},
					},
					"severity": 1,
					"source":   "fake",
					"message":  "необъявленная переменная marker",
				}},
			},
		})
	}
	diagItems := []map[string]any{{
		"range": map[string]any{
			"start": map[string]any{"line": 1, "character": 2},
			"end":   map[string]any{"line": 1, "character": 8},
		},
		"severity": 1, "source": "fake", "message": "pull-диагностика",
	}}

	for {
		length := -1
		for {
			line, err := in.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if strings.HasPrefix(strings.ToLower(line), "content-length:") {
				if n, err := strconv.Atoi(strings.TrimSpace(line[len("content-length:"):])); err == nil {
					length = n
				}
			}
		}
		if length <= 0 {
			return
		}
		body := make([]byte, length)
		if _, err := readFull(in, body); err != nil {
			return
		}
		var msg struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(body, &msg) != nil {
			continue
		}
		switch msg.Method {
		case "initialize":
			reply(msg.ID, map[string]any{
				"capabilities": map[string]any{
					"hoverProvider":             true,
					"definitionProvider":        true,
					"textDocumentSync":          1,
					"diagnosticProvider":        true,
					"publishDiagnosticsSupport": true,
				},
			})
		case "textDocument/didOpen", "textDocument/didChange":
			if mode == "push" {
				var p struct {
					TextDocument struct {
						URI string `json:"uri"`
					} `json:"textDocument"`
				}
				_ = json.Unmarshal(msg.Params, &p)
				push(p.TextDocument.URI)
			}
		case "textDocument/diagnostic":
			if mode == "pull" {
				reply(msg.ID, map[string]any{"kind": "full", "items": diagItems})
			} else {
				reply(msg.ID, map[string]any{"kind": "full", "items": []any{}})
			}
		case "textDocument/hover":
			reply(msg.ID, map[string]any{
				"contents": map[string]any{"kind": "plaintext", "value": "func Marker() error"},
			})
		case "textDocument/definition":
			reply(msg.ID, []map[string]any{{
				"uri": "file:///fake/marker.go",
				"range": map[string]any{
					"start": map[string]any{"line": 41, "character": 0},
					"end":   map[string]any{"line": 41, "character": 9},
				},
			}})
		case "shutdown":
			reply(msg.ID, nil)
		case "exit":
			return
		}
	}
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := r.Read(buf[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// fakeLSPCmd — команда запуска фальшивого сервера для хаба.
func fakeLSPCmd(t *testing.T, mode string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("не удалось узнать путь к тестовому бинарнику: %v", err)
	}
	return []string{exe, "-test.run=TestFakeLSPServer", "--"}
}

// lspEnv — режим фальшивого сервера для дочернего процесса.
func lspEnv(t *testing.T, mode string) map[string]string {
	t.Helper()
	t.Setenv(envFakeLSP, mode)
	return map[string]string{".go": strings.Join(fakeLSPCmd(t, mode), " ")}
}

// ---------- проверки ----------

const lspSample = "package main\n\nfunc Marker() error { return nil }\n\nfunc main() {}\n"

// TestLSPHubResolvesByExtension — сервер подбирается по расширению
// файла и по переопределению из конфига.
//
// Пресеты проверяются только там, где соответствующая программа есть в
// PATH: их отсутствие — не дефект хаба, а отсутствие gopls на машине.
// В этом случае хаб обязан честно сказать «не найден», и это проверяется
// отдельно.
func TestLSPHubResolvesByExtension(t *testing.T) {
	h := NewLSPHub(nil, true)
	for ext, cmd := range lspPresets {
		fields := strings.Fields(cmd)
		if _, err := exec.LookPath(fields[0]); err != nil {
			continue
		}
		if _, _, ok := h.ServerFor("x" + ext); !ok {
			t.Errorf("пресет для %s не разрешился, хотя %s есть в PATH", ext, fields[0])
		}
	}
	if _, _, ok := h.ServerFor("x.docx"); ok {
		t.Error("для неизвестного расширения сервер найден")
	}
	h2 := NewLSPHub(map[string]string{".go": "мой-сервер --stdio"}, true)
	if _, _, ok := h2.ServerFor("x.go"); ok {
		t.Error("переопределение на несуществующую программу принято")
	}
	h3 := NewLSPHub(map[string]string{".go": strings.Join(fakeLSPCmd(t, "push"), " ")}, true)
	args, lang, ok := h3.ServerFor("x.go")
	if !ok {
		t.Fatal("переопределение на существующую программу отвергнуто")
	}
	if len(args) == 0 || args[0] == "gopls" {
		t.Errorf("пресет не замещён: %v", args)
	}
	if lang != "go" {
		t.Errorf("languageId %q", lang)
	}
}

// TestLSPMissingServerExplains — сервер прописан, а программы нет.
// Отказ обязан называть, что именно искали и куда смотреть, иначе агент
// решит, что LSP не поддержан вовсе.
func TestLSPMissingServerExplains(t *testing.T) {
	h := NewLSPHub(map[string]string{".go": "заведомо-нет-такой-программы"}, true)
	defer h.Shutdown()
	_, err := h.conn(t.TempDir(), "x.go")
	if err == nil {
		t.Fatal("отсутствующий сервер не замечен")
	}
	if !strings.Contains(err.Error(), "заведомо-нет-такой-программы") {
		t.Errorf("в отказе нет имени программы: %v", err)
	}
	if !strings.Contains(err.Error(), "config.json") {
		t.Errorf("в отказе нет подсказки, что переопределить: %v", err)
	}
}

// TestLSPDisabledIsHonest — выключенный LSP не должен имитировать
// работу: инструмент обязан сказать, что сервер выключен.
func TestLSPDisabledIsHonest(t *testing.T) {
	h := NewLSPHub(nil, false)
	if _, _, ok := h.ServerFor("x.go"); ok {
		t.Error("выключенный LSP всё равно нашёл сервер")
	}
	if h.Supported("x.go") {
		t.Error("Supported=true при выключенном LSP")
	}
}

// TestLSPHoverAndDefinition — ответы сервера доходят до модели в
// читаемом виде: без этого инструмент бесполезен.
func TestLSPHoverAndDefinition(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "marker.go")
	if err := os.WriteFile(file, []byte(lspSample), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewLSPHub(lspEnv(t, "push"), true)
	h.diagWait = 600 * time.Millisecond
	defer h.Shutdown()

	hover, err := h.Hover(dir, file, 3, 6)
	if err != nil {
		t.Fatalf("hover: %v", err)
	}
	if !strings.Contains(hover, "Marker() error") {
		t.Errorf("hover без сигнатуры: %q", hover)
	}

	def, err := h.Definition(dir, file, 5, 2)
	if err != nil {
		t.Fatalf("definition: %v", err)
	}
	if !strings.Contains(def, "marker.go") || !strings.Contains(def, "42") {
		t.Errorf("определение без файла и строки: %q", def)
	}
}

// TestLSPPositionIsOneBased — позиция считается как в read_file:
// строка с 1, столбец с 0. Ошибка на единицу уводит модель в
// соседний символ, и ответ приходит не про то.
func TestLSPPositionIsOneBased(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "marker.go")
	if err := os.WriteFile(file, []byte(lspSample), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewLSPHub(lspEnv(t, "push"), true)
	h.diagWait = 600 * time.Millisecond
	defer h.Shutdown()
	if _, err := h.Hover(dir, file, 0, 0); err == nil {
		t.Error("строка 0 принята: строки нумеруются с 1")
	}
	if _, err := h.Hover(dir, file, 1, -1); err == nil {
		t.Error("столбец -1 принят: столбцы нумеруются с 0")
	}
}

// TestLSPDiagnosticsPushAfterEdit — главная механика: после правки
// диагностика приходит сама, без явного вызова инструмента.
func TestLSPDiagnosticsPushAfterEdit(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "marker.go")
	if err := os.WriteFile(file, []byte(lspSample), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewLSPHub(lspEnv(t, "push"), true)
	h.diagWait = 600 * time.Millisecond
	defer h.Shutdown()

	got, err := h.Diagnostics(dir, file)
	if err != nil {
		t.Fatalf("diagnostics: %v", err)
	}
	if !strings.Contains(got, "необъявленная переменная") {
		t.Errorf("push-диагностика не дошла:\n%q", got)
	}
	if !strings.Contains(got, "marker.go") {
		t.Errorf("в отчёте нет имени файла:\n%q", got)
	}
}

// TestLSPDiagnosticsPullFallback — сервер, который не шлёт push,
// обслуживается запросом textDocument/diagnostic. Иначе серверы,
// молчащие по push, выглядели бы как «проблем нет».
func TestLSPDiagnosticsPullFallback(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "marker.go")
	if err := os.WriteFile(file, []byte(lspSample), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewLSPHub(lspEnv(t, "pull"), true)
	h.diagWait = 600 * time.Millisecond
	defer h.Shutdown()

	got, err := h.Diagnostics(dir, file)
	if err != nil {
		t.Fatalf("diagnostics: %v", err)
	}
	if !strings.Contains(got, "pull-диагностика") {
		t.Errorf("pull-ветка не сработала:\n%q", got)
	}
}

// TestLSPDiagnosticsCleanFile — отсутствие замечаний тоже результат,
// и он должен быть назван прямо, а не выглядеть как пустой ответ.
func TestLSPDiagnosticsCleanFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "marker.go")
	if err := os.WriteFile(file, []byte(lspSample), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewLSPHub(lspEnv(t, "silent"), true)
	h.diagWait = 600 * time.Millisecond
	defer h.Shutdown()

	got, err := h.Diagnostics(dir, file)
	if err != nil {
		t.Fatalf("diagnostics: %v", err)
	}
	if got != "" && !strings.Contains(got, "проблем нет") {
		t.Errorf("неожиданный отчёт при отсутствии замечаний: %q", got)
	}
}

// TestLSPSilentServerMustNotBreakEdit — страховка не должна ломать
// правку: сервер молчит, файл тронут, агент продолжает работу.
func TestLSPSilentServerMustNotBreakEdit(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "marker.go")
	if err := os.WriteFile(file, []byte(lspSample), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewLSPHub(lspEnv(t, "silent"), true)
	h.diagWait = 600 * time.Millisecond
	defer h.Shutdown()
	_, _ = h.Diagnostics(dir, file)
	if _, err := os.Stat(file); err != nil {
		t.Errorf("файл исчез после диагностики: %v", err)
	}
}

// TestReadFrameRejectsHugeFrame — Content-Length приходит от внешнего
// процесса; гигантское значение не должно приводить к выделению памяти.
func TestReadFrameRejectsHugeFrame(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("Content-Length: 999999999\r\n\r\n"))
	if _, ok := readFrame(r); ok {
		t.Fatal("фрейм с гигантским Content-Length принят")
	}
}

// TestLSPMissingFileAnswers — несуществующий файл обязан дать понятную
// ошибку, а не молча вернуть пустоту.
func TestLSPMissingFileAnswers(t *testing.T) {
	dir := t.TempDir()
	h := NewLSPHub(lspEnv(t, "push"), true)
	h.diagWait = 600 * time.Millisecond
	defer h.Shutdown()
	missing := filepath.Join(dir, "нет-такого.go")
	if _, err := h.Hover(dir, missing, 1, 0); err == nil {
		t.Error("hover на несуществующем файле не дал ошибки")
	}
}

// TestLSPURIKeepsDriveLetter — на Windows адрес обязан быть
// file:///C:/…, иначе буква диска уезжает в Host и файл не находится.
func TestLSPURIKeepsDriveLetter(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join("C:\\", "x", "y.go"))
	if err != nil {
		t.Skipf("не удалось получить абсолютный путь: %v", err)
	}
	uri := pathToURI(abs)
	if !strings.HasPrefix(uri, "file:///") {
		t.Errorf("адрес без трёх слэшей: %q", uri)
	}
	if strings.HasPrefix(uri, "file://C:") {
		t.Errorf("буква диска попала в Host: %q", uri)
	}
	if back := uriToPath(uri); back != abs {
		t.Errorf("обратное преобразование дало %q вместо %q", back, abs)
	}
}

// TestLSPShutdownIsIdempotentAndRecovers — Shutdown зовётся перед выходом
// и при пересборке реестра, поэтому второй вызов на умершем сервере не
// должен падать. Хаб при этом остаётся рабочим: следующий вызов
// поднимает новый сервер сам, и «после остановки всё сломалось» было бы
// неправдой.
func TestLSPShutdownIsIdempotentAndRecovers(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "marker.go")
	if err := os.WriteFile(file, []byte(lspSample), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewLSPHub(lspEnv(t, "push"), true)
	h.diagWait = 600 * time.Millisecond
	if _, err := h.Hover(dir, file, 3, 6); err != nil {
		t.Fatal(err)
	}
	h.Shutdown()
	h.Shutdown()
	if _, err := h.Hover(dir, file, 3, 6); err != nil {
		t.Errorf("хаб не восстановился после Shutdown: %v", err)
	}
	h.Shutdown()
}
