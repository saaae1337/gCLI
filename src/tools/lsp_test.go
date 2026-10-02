package tools

// Тесты LSP-механики. Настоящие language серверы в тесты не зовутся:
// поднимается фейковый сервер на os.Pipe, который честно говорит по
// протоколу (initialize → publishDiagnostics → textDocument/hover).
// Так проверяется наш фрейминг и маршрутизация, а не чужие серверы.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPathURIRoundTrip(t *testing.T) {
	wd := t.TempDir()
	p := wd + "/sub/file main.go" // пробел в имени — классическая ловушка экранирования
	uri := pathToURI(p)
	if !strings.HasPrefix(uri, "file://") {
		t.Fatalf("не file:// URI: %q", uri)
	}
	if got := uriToPath(uri); got != p {
		t.Fatalf("round trip: %q → %q → %q", p, uri, got)
	}
	// Относительный URI без scheme не превращаем молча.
	if got := uriToPath("http://example.com/x.go"); got != "http://example.com/x.go" {
		t.Fatalf("не-file URI должен пройти насквозь, получили %q", got)
	}
}

func TestUTF16ColumnConversion(t *testing.T) {
	line := "func (\U0001F600) abc" // эмодзи за пределами BMP: 1 rune, 2 UTF-16 единицы
	if got := utf16ColOf(line, 6); got != 6 {
		t.Fatalf("utf16ColOf до эмодзи: %d, ждали 6", got)
	}
	if got := utf16ColOf(line, 7); got != 8 {
		t.Fatalf("utf16ColOf после эмодзи: %d, ждали 8 (2 единицы на символ)", got)
	}
	if got := runeColOf(line, 8); got != 7 {
		t.Fatalf("runeColOf: %d, ждали 7", got)
	}
	if got := runeColOf(line, 6); got != 6 {
		t.Fatalf("runeColOf до эмодзи: %d, ждали 6", got)
	}
}

func TestServerForPresetAndConfig(t *testing.T) {
	// Переопределение указывает на тестовый бинарник (существует точно):
	// ServerFor обязан проверять наличие бинарника, а не только пресет.
	h := NewLSPHub(map[string]string{".nim": os.Args[0]}, true)
	if _, _, ok := h.ServerFor("x.nim"); !ok {
		t.Fatal("переопределение с существующим бинарником должно работать")
	}
	hmiss := NewLSPHub(map[string]string{".nim": "точно-нет-такого-бинарника-xyz"}, true)
	if _, _, ok := hmiss.ServerFor("x.nim"); ok {
		t.Fatal("переопределение с несуществующим бинарником не должно проходить")
	}
	if _, _, ok := h.ServerFor("x.unsupported"); ok {
		t.Fatal("непокрытое расширение не должно иметь сервера")
	}
	off := NewLSPHub(nil, false)
	if _, _, ok := off.ServerFor("x.go"); ok {
		t.Fatal("выключенный хаб не должен отдавать серверы")
	}
}

func TestLSPToolsHonestWhenNoHub(t *testing.T) {
	dir := t.TempDir()
	r := New(Env{WorkDir: dir})
	p := dir + "/main.go"
	_ = os.WriteFile(p, []byte("package main\n"), 0o644)
	_, err := r.hLSPDiagnostics(context.Background(), map[string]any{"path": p})
	// Хаб nil → честная ошибка, а не паника или тихая пустышка.
	if err == nil || !strings.Contains(err.Error(), "LSP") {
		t.Fatalf("ждали ошибку про LSP, получили: %v", err)
	}
}

// fakeLSPServer — минимальный сервер: отвечает на initialize, шлёт
// publishDiagnostics после didOpen, отвечает на hover.
func fakeLSPServer(t *testing.T) (clientR, clientW *os.File, stop func()) {
	t.Helper()
	serverR, clientW, err := os.Pipe() // сервер читает, клиент пишет
	if err != nil {
		t.Fatal(err)
	}
	clientR, serverW, err := os.Pipe() // клиент читает, сервер пишет
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		r := bufio.NewReader(serverR)
		w := serverW
		for {
			body, ok := readFrame(r)
			if !ok {
				return
			}
			var msg lspMessage
			if json.Unmarshal(body, &msg) != nil {
				continue
			}
			switch msg.Method {
			case "initialize":
				resp, _ := json.Marshal(lspMessage{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(`{"capabilities":{}}`)})
				_ = writeFrame(w, resp)
			case "initialized":
				// Никаких действий: нотификация.
			case "textDocument/didOpen":
				var p struct {
					TextDocument struct {
						URI string `json:"uri"`
					} `json:"textDocument"`
				}
				_ = json.Unmarshal(msg.Params, &p)
				notif, _ := json.Marshal(map[string]any{
					"uri": p.TextDocument.URI,
					"diagnostics": []map[string]any{{
						"range":    map[string]any{"start": map[string]any{"line": 1, "character": 0}},
						"severity": 1,
						"message":  "undefined: foo",
						"source":   "fake",
					}},
				})
				out, _ := json.Marshal(lspMessage{JSONRPC: "2.0", Method: "textDocument/publishDiagnostics", Params: notif})
				_ = writeFrame(w, out)
			case "textDocument/hover":
				resp, _ := json.Marshal(lspMessage{JSONRPC: "2.0", ID: msg.ID,
					Result: json.RawMessage(`{"contents":{"kind":"markdown","value":"func foo()"}}`)})
				_ = writeFrame(w, resp)
			case "shutdown":
				resp, _ := json.Marshal(lspMessage{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage("null")})
				_ = writeFrame(w, resp)
			case "exit":
				return
			default:
				if msg.ID != 0 {
					resp, _ := json.Marshal(lspMessage{JSONRPC: "2.0", ID: msg.ID, Error: &lspError{Code: -32601, Message: "not found"}})
					_ = writeFrame(w, resp)
				}
			}
		}
	}()
	return clientR, clientW, func() {
		_ = clientW.Close()
		_ = clientR.Close()
		_ = serverW.Close()
		_ = serverR.Close()
	}
}

func TestLSPConnAgainstFakeServer(t *testing.T) {
	clientR, clientW, stop := fakeLSPServer(t)
	defer stop()
	dir := t.TempDir()
	c := newLSPConn(clientR, clientW, nil, dir, "go")

	p := dir + "/main.go"
	if err := os.WriteFile(p, []byte("package main\n\nvar x = foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// didOpen → фейк шлёт publishDiagnostics → наш сборщик его ловит.
	out := c.diagnosticsAfterEdit(p, 3*time.Second)
	if !strings.Contains(out, "ошибка") || !strings.Contains(out, "undefined: foo") {
		t.Fatalf("диагностика не дошла: %q", out)
	}
	if !strings.Contains(out, "2:0") {
		t.Fatalf("строка диагностики неверна: %q", out)
	}

	hov, err := c.hover(p, 1, 0)
	if err != nil || !strings.Contains(hov, "func foo()") {
		t.Fatalf("hover: %q err=%v", hov, err)
	}
	c.stop()
}

func TestFormatDiagnosticsEmptyAndMany(t *testing.T) {
	if got := formatDiagnostics("/x/a.go", "package main\n", nil); !strings.Contains(got, "проблем нет") {
		t.Fatalf("пустые диагностики: %q", got)
	}
	var items []lspDiag
	for i := 0; i < 25; i++ {
		var d lspDiag
		d.Message = fmt.Sprintf("проблема %d", i)
		d.Range.Start.Line = i
		items = append(items, d)
	}
	got := formatDiagnostics("/x/a.go", strings.Repeat("line\n", 30), items)
	if !strings.Contains(got, "и ещё 5") {
		t.Fatalf("список не обрезан до 20: %q", got)
	}
}

// Заглушки интерфейсов для newLSPConn без настоящих процессов.
type stopWriter struct{}

func (stopWriter) Write(p []byte) (int, error) { return len(p), nil }
func (stopWriter) Close() error                { return nil }

type noopCloser struct{}

func (noopCloser) Close() error { return nil }
