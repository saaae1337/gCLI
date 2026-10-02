package tools

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadMCPConfigMerge — конфиги глобальный + проектный сливаются,
// проектный перекрывает глобальный по имени сервера.
func TestLoadMCPConfigMerge(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()

	globalDir := filepath.Join(home, "mcp.json")
	if err := os.WriteFile(globalDir, []byte(`{"servers":{
		"fs": {"command":"npx","args":["-y","server-fs"]},
		"old": {"command":"node","args":["old.js"]}
	}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	projDir := filepath.Join(dir, ".gcli", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(projDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projDir, []byte(`{"servers":{
		"fs": {"command":"npx","args":["-y","server-fs","."],"timeout_sec":60}
	}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := New(Env{WorkDir: dir})
	// Подменяем каталог глобального конфига: MCPDirs читает core.Home().
	// Проверяем через RegisterMCPVersion нельзя (запустит процессы), поэтому
	// вызываем LoadMCPConfig с подменённым HOME через mcpDirsOverride.
	restore := overrideHome(home)
	defer restore()

	cfg, warns := r.LoadMCPConfig()
	if len(warns) != 0 {
		t.Fatalf("неожиданные предупреждения: %v", warns)
	}
	if len(cfg.Servers) != 2 {
		t.Fatalf("серверов: %d, ожидалось 2 (fs + old): %+v", len(cfg.Servers), cfg.Servers)
	}
	if cfg.Servers["fs"].TimeoutSec != 60 || len(cfg.Servers["fs"].Args) != 3 {
		t.Errorf("проектный сервер fs не перекрыл глобальный: %+v", cfg.Servers["fs"])
	}
	if _, ok := cfg.Servers["old"]; !ok {
		t.Error("глобальный сервер old потерялся")
	}
}

// TestLoadMCPConfigBadJSON — битый конфиг даёт предупреждение, не панику.
func TestLoadMCPConfigBadJSON(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	projDir := filepath.Join(dir, ".gcli", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(projDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projDir, []byte(`{"servers": СЛОМАНО`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(Env{WorkDir: dir})
	restore := overrideHome(home)
	defer restore()
	_, warns := r.LoadMCPConfig()
	if len(warns) == 0 {
		t.Error("битый mcp.json должен давать предупреждение")
	}
}

// TestMCPToolNameSanitized — составное имя инструмента безопасно.
func TestMCPToolNameSanitized(t *testing.T) {
	name := MCPToolName("My Server", "Get-Weather!")
	if name != "mcp__my_server__get_weather" {
		t.Errorf("MCPToolName = %q", name)
	}
	if MCPToolName("a", "b") != "mcp__a__b" {
		t.Error("базовое имя искажено")
	}
}

// TestMCPParseCallResult — разбор ответа tools/call: текст, картинки, ошибка.
func TestMCPParseCallResult(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": "привет"},
			{"type": "image", "data": base64.StdEncoding.EncodeToString([]byte("png")), "mimeType": "image/png"},
		},
	})
	res, err := mcpParseCallResult(raw, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "привет" {
		t.Errorf("текст: %q", res.Text)
	}
	if len(res.Images) != 1 || res.Images[0].MIME != "image/png" {
		t.Errorf("картинки: %+v", res.Images)
	}

	// isError=true → ошибка уходит в Result.Error.
	rawErr, _ := json.Marshal(map[string]any{
		"isError": true,
		"content": []map[string]any{{"type": "text", "text": "упало"}},
	})
	res2, err := mcpParseCallResult(rawErr, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if res2.Error == "" || res2.Text != "" {
		t.Errorf("isError не обработан: %+v", res2)
	}

	// Пустой ответ.
	rawEmpty, _ := json.Marshal(map[string]any{"content": []map[string]any{}})
	res3, _ := mcpParseCallResult(rawEmpty, "srv")
	if res3.Text == "" {
		t.Error("пустой ответ должен давать заглушку")
	}
}

// TestMCPParseCallResultBadJSON — кривой ответ сервера даёт ошибку, не панику.
func TestMCPParseCallResultBadJSON(t *testing.T) {
	if _, err := mcpParseCallResult(json.RawMessage("{нет"), "srv"); err == nil {
		t.Error("ожидалась ошибка разбора")
	}
}

// overrideHome — подменить каталог данных gcli на время теста.
func overrideHome(home string) func() {
	old := os.Getenv("GCLI_HOME")
	os.Setenv("GCLI_HOME", home)
	return func() { os.Setenv("GCLI_HOME", old) }
}
