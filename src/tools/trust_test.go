package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------- Доверие коду из проекта ----------
//
// Клон репозитория не должен приносить исполняемый код на машину. Тесты
// проверяют ровно это: расширение и MCP-сервер из .gcli не подключаются без
// согласия, подтверждение привязано к содержимому файла, а глобальный
// (пользовательский) код подключается без вопросов.

const testExtJSON = `{
  "name": "%s",
  "description": "расширение из проекта",
  "tools": [
    {"name": "%s_probe", "description": "проверка", "command": "echo привет"}
  ]
}`

// writeProjectExt — положить манифест расширения в .gcli/extensions проекта.
func writeProjectExt(t *testing.T, dir, name string) string {
	t.Helper()
	d := filepath.Join(dir, ".gcli", "extensions")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(d, name+".json")
	body := strings.ReplaceAll(strings.ReplaceAll(testExtJSON, "%s", name), "%s_probe", name+"_probe")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// newTrustReg — реестр проекта с отдельным каталогом данных и хранилищем
// доверия. Каталог данных отдельный, чтобы тест не писал в ~/.gcli.
func newTrustReg(t *testing.T, dir string) *Registry {
	t.Helper()
	home := t.TempDir()
	return New(Env{WorkDir: dir, Trust: NewTrustStore(home)})
}

// TestProjectExtNotLoadedWithoutTrust — главный тест: расширение из проекта
// не подключается само, даже если реестр собран полностью.
func TestProjectExtNotLoadedWithoutTrust(t *testing.T) {
	dir := t.TempDir()
	writeProjectExt(t, dir, "risky")
	r := newTrustReg(t, dir)

	_, warns := r.LoadExtensionTools()
	if n := r.ExtCount(); n != 0 {
		t.Fatalf("инструменты недоверенного расширения подключены: %d", n)
	}
	joined := strings.Join(warns, "\n")
	if !strings.Contains(joined, "risky") {
		t.Errorf("нет предупреждения о недоверенном расширении:\n%s", joined)
	}
	pending := r.ScanProjectCode()
	if len(pending) != 1 || pending[0].Kind != "ext" || pending[0].Name != "risky" {
		t.Fatalf("ScanProjectCode не нашёл расширение: %+v", pending)
	}
	if pending[0].Hash == "" {
		t.Error("у недоверенного кода должен быть отпечаток — по нему его подтверждают")
	}
}

// TestTrustThenExtLoads — после согласия расширение подключается молча.
func TestTrustThenExtLoads(t *testing.T) {
	dir := t.TempDir()
	writeProjectExt(t, dir, "risky")
	r := newTrustReg(t, dir)

	w, ok := r.FindPendingCode("ext", "risky")
	if !ok {
		t.Fatal("расширение не найдено среди недоверенных")
	}
	r.TrustCode(w)

	if got := r.ScanProjectCode(); len(got) != 0 {
		t.Errorf("после подтверждения расширение всё ещё ждёт: %+v", got)
	}
	r.LoadExtensionTools()
	if n := r.ExtCount(); n != 1 {
		t.Fatalf("после подтверждения подключено инструментов %d, ожидался 1", n)
	}
	if r.Get("risky_probe") == nil {
		t.Error("инструмент подтверждённого расширения не зарегистрирован")
	}
}

// TestTrustInvalidatedByEdit — правка манифеста отменяет согласие. Иначе
// подтвердить безобидное расширение, а потом тихо дописать в него команду —
// значит вообще ничего не подтверждать.
func TestTrustInvalidatedByEdit(t *testing.T) {
	dir := t.TempDir()
	p := writeProjectExt(t, dir, "risky")
	r := newTrustReg(t, dir)
	r.TrustCode(mustPending(t, r, "ext", "risky"))

	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(body, []byte("\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	pending := r.ScanProjectCode()
	if len(pending) != 1 {
		t.Fatalf("после правки манифеста согласие должно было слететь: %+v", pending)
	}
	r.LoadExtensionTools()
	if n := r.ExtCount(); n != 0 {
		t.Fatalf("изменённое расширение подключено: %d инструментов", n)
	}
}

// TestUntrustForgetsCode — /permissions reset отключает доверенный код.
func TestUntrustForgetsCode(t *testing.T) {
	dir := t.TempDir()
	writeProjectExt(t, dir, "risky")
	r := newTrustReg(t, dir)
	r.TrustCode(mustPending(t, r, "ext", "risky"))

	if n := r.UntrustCode(); n != 1 {
		t.Fatalf("забыто подтверждений: %d, ожидалась 1", n)
	}
	if len(r.TrustedList()) != 0 {
		t.Errorf("список доверенного не пуст: %v", r.TrustedList())
	}
	r.LoadExtensionTools()
	if n := r.ExtCount(); n != 0 {
		t.Errorf("после сброса расширение всё ещё подключено: %d", n)
	}
}

// TestTrustPersistsAcrossRestarts — согласие переживает перезапуск: иначе
// пришлось бы подтверждать одно и то же расширение каждый запуск.
func TestTrustPersistsAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	writeProjectExt(t, dir, "risky")
	home := t.TempDir()

	r1 := New(Env{WorkDir: dir, Trust: NewTrustStore(home)})
	r1.TrustCode(mustPending(t, r1, "ext", "risky"))

	r2 := New(Env{WorkDir: dir, Trust: NewTrustStore(home)})
	r2.LoadExtensionTools()
	if n := r2.ExtCount(); n != 1 {
		t.Fatalf("после перезапуска расширение не подключилось: %d инструментов", n)
	}
	if _, err := os.Stat(filepath.Join(home, "trusted.json")); err != nil {
		t.Errorf("файл согласий не создан: %v", err)
	}
}

// TestGlobalExtTrustedWithoutPrompt — расширение пользователя (~/.gcli)
// подключается без согласия: писал его сам пользователь.
func TestGlobalExtTrustedWithoutPrompt(t *testing.T) {
	dir := t.TempDir()
	r := newTestReg(t, dir)
	r.env.Trust = NewTrustStore(t.TempDir())

	// Подменяем глобальный каталог расширений на временный.
	gdir := filepath.Join(t.TempDir(), "extensions")
	if err := os.MkdirAll(gdir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.ReplaceAll(testExtJSON, "%s", "mine")
	body = strings.ReplaceAll(body, "%s_probe", "mine_probe")
	if err := os.WriteFile(filepath.Join(gdir, "mine.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	setGlobalExtDir(t, gdir)

	r.LoadExtensionTools()
	if n := r.ExtCount(); n != 1 {
		t.Fatalf("глобальное расширение должно подключаться без согласия: %d инструментов", n)
	}
	if got := r.ScanProjectCode(); len(got) != 0 {
		t.Errorf("глобальное расширение попало в «требует подтверждения»: %+v", got)
	}
}

// TestMCPServerNeedsTrust — MCP-сервер из проекта не запускается без
// согласия, даже если команда описана корректно.
func TestMCPServerNeedsTrust(t *testing.T) {
	dir := t.TempDir()
	writeProjectMCP(t, dir, "evil")
	r := newTrustReg(t, dir)

	if r.mcpTrusted("evil") {
		t.Fatal("проектный MCP-сервер не должен считаться доверенным без согласия")
	}
	n, warns := r.RegisterMCP()
	if n != 0 {
		t.Errorf("подключено инструментов: %d", n)
	}
	if !strings.Contains(strings.Join(warns, "\n"), "evil") {
		t.Errorf("нет предупреждения о недоверенном сервере:\n%s", strings.Join(warns, "\n"))
	}
	pending := r.ScanProjectCode()
	if len(pending) != 1 || pending[0].Kind != "mcp" || pending[0].Name != "evil" {
		t.Fatalf("ScanProjectCode не нашёл сервер: %+v", pending)
	}
}

// TestMCPProjectShadowsGlobalTrust — проектный сервер с тем же именем, что и
// глобальный, не должен выполняться под крышкой глобального доверия:
// LoadMCPConfig отдаст определение из проекта.
func TestMCPProjectShadowsGlobalTrust(t *testing.T) {
	dir := t.TempDir()
	writeProjectMCP(t, dir, "dual")
	r := newTrustReg(t, dir)

	gpath := filepath.Join(t.TempDir(), "mcp.json")
	writeMCPFile(t, gpath, map[string]MCPServer{
		"dual": {Command: "echo", Args: []string{"глобальный"}},
	})
	setGlobalMCPPath(t, gpath)

	// Определение берётся из проекта, значит и доверие должно проверяться
	// по проекту.
	if cfg, _ := r.LoadMCPConfig(); cfg.Servers["dual"].Args[0] != "evil" {
		t.Fatalf("проектный конфиг не перекрыл глобальный: %+v", cfg.Servers["dual"])
	}
	if r.mcpTrusted("dual") {
		t.Error("проектный сервер унаследовал доверие глобального")
	}
}

// TestMCPTrustAppliesToWholeFile — отпечаток берётся по файлу целиком: рядом
// появившийся второй сервер тоже требует подтверждения.
func TestMCPTrustAppliesToWholeFile(t *testing.T) {
	dir := t.TempDir()
	writeProjectMCP(t, dir, "first")
	r := newTrustReg(t, dir)
	r.TrustCode(mustPending(t, r, "mcp", "first"))

	if !r.mcpTrusted("first") {
		t.Fatal("подтверждённый сервер должен считаться доверенным")
	}
	// Дописываем в тот же файл второй сервер.
	p := filepath.Join(dir, ".gcli", "mcp.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var cfg MCPConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Servers["second"] = MCPServer{Command: "echo", Args: []string{"подставной"}}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	if r.mcpTrusted("first") {
		t.Error("после правки mcp.json согласие должно было слететь")
	}
	if r.mcpTrusted("second") {
		t.Error("новый сервер в том же файле не должен быть доверенным")
	}
}

// TestTrustAbsentStoreSkipsProjectCode — без хранилища доверия проектный код
// не подключается: «забыли спросить» — худший вариант.
func TestTrustAbsentStoreSkipsProjectCode(t *testing.T) {
	dir := t.TempDir()
	writeProjectExt(t, dir, "risky")
	r := New(Env{WorkDir: dir}) // Trust не задан

	if trusted, _ := r.extTrusted(Extension{Name: "risky"}); trusted {
		t.Error("расширение из проекта подключено без хранилища доверия")
	}
	if r.mcpTrusted("что-то") {
		t.Error("MCP из проекта доверен без хранилища доверия")
	}
}

// mustPending — найти недоверенный код или упасть.
func mustPending(t *testing.T, r *Registry, kind, name string) ProjectCodeWarning {
	t.Helper()
	w, ok := r.FindPendingCode(kind, name)
	if !ok {
		t.Fatalf("не найден недоверенный %s «%s»", kind, name)
	}
	return w
}
