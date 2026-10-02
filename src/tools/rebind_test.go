package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

// Обработчики встроенных инструментов — связанные методы (r.hReadFile), то
// есть они навсегда захватывают реестр, в котором были зарегистрированы.
// Копии реестра (Base/Restrict/Merge) обязаны пересобирать обработчики на
// себя: иначе инструмент копии работает с чужим состоянием, а изоляция,
// ради которой копии и создаются, оказывается фикцией.

// TestSubagentAskTraceIsIsolated — ask_trace субагента не показывает вопросы
// главного агента. Иначе субагент принимал бы чужие ответы за свои и решал
// задачу по чужому контексту.
func TestSubagentAskTraceIsIsolated(t *testing.T) {
	r := newTestReg(t, t.TempDir())
	sub := r.Base()

	r.recordAsk("вопрос главного", "ответ главного", false)
	sub.recordAsk("вопрос субагента", "ответ субагента", false)

	subRes := mustRun(t, sub, "ask_trace", nil)
	if !strings.Contains(subRes.Text, "вопрос субагента") {
		t.Errorf("субагент не видит свой вопрос:\n%s", subRes.Text)
	}
	if strings.Contains(subRes.Text, "вопрос главного") {
		t.Errorf("субагент видит чужой след:\n%s", subRes.Text)
	}

	mainRes := mustRun(t, r, "ask_trace", nil)
	if !strings.Contains(mainRes.Text, "вопрос главного") {
		t.Errorf("главный агент потерял свой след:\n%s", mainRes.Text)
	}
	if strings.Contains(mainRes.Text, "вопрос субагента") {
		t.Errorf("вопрос субагента протекает в главный след:\n%s", mainRes.Text)
	}
}

// TestRestrictAskTraceIsIsolated — то же для реестра с ограничением набора
// инструментов: им пользуется реальный субагент.
func TestRestrictAskTraceIsIsolated(t *testing.T) {
	r := newTestReg(t, t.TempDir())
	sub := r.Base().Restrict(nil, nil)

	r.recordAsk("вопрос главного", "ответ главного", false)
	sub.recordAsk("вопрос субагента", "ответ субагента", false)

	subRes := mustRun(t, sub, "ask_trace", nil)
	if strings.Contains(subRes.Text, "вопрос главного") {
		t.Errorf("Restrict не изолировал след вопросов:\n%s", subRes.Text)
	}
	if !strings.Contains(subRes.Text, "вопрос субагента") {
		t.Errorf("субагент не видит свой след:\n%s", subRes.Text)
	}
}

// TestMergeAskTraceIsIsolated — объединённый реестр агрегирует инструменты
// нескольких субагентов, и смешивать их ответы нельзя.
func TestMergeAskTraceIsIsolated(t *testing.T) {
	r := newTestReg(t, t.TempDir())
	r.recordAsk("вопрос главного", "ответ главного", false)

	merged := r.Merge(r.Base())
	merged.recordAsk("вопрос объединённого", "ответ объединённого", false)

	res := mustRun(t, merged, "ask_trace", nil)
	if !strings.Contains(res.Text, "вопрос объединённого") {
		t.Errorf("объединённый реестр не записал свой вопрос:\n%s", res.Text)
	}
	if strings.Contains(res.Text, "вопрос главного") {
		t.Errorf("Merge не изолировал след вопросов:\n%s", res.Text)
	}
}

// TestSubagentReadFilesIsIsolated — read_file субагента не должен помечать
// файл прочитанным в карте главного агента. Иначе главный агент получил бы
// право редактировать файл, которого не читал, и edit_file прошёл бы мимо
// обязательного чтения.
func TestSubagentReadFilesIsIsolated(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	mustWrite(t, p, "содержимое\n")

	r := newTestReg(t, dir)
	sub := r.Base().Restrict(nil, nil)

	if _, err := run(t, sub, "read_file", map[string]any{"path": "a.txt"}); err != nil {
		t.Fatalf("read_file субагента вернул ошибку: %v", err)
	}
	if r.env.ReadFiles[p] {
		t.Error("чтение субагента пометило файл в карте главного агента")
	}
	if !sub.env.ReadFiles[p] {
		t.Error("субагент не пометил файл в своей карте")
	}
}

// TestEditFileStillRequiresOwnRead — субагент не наследует право правки от
// главного агента: edit_file требует чтения именно в своём реестре.
func TestEditFileStillRequiresOwnRead(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "старое\n")

	r := newTestReg(t, dir)
	sub := r.Base().Restrict(nil, nil)

	mustRun(t, r, "read_file", map[string]any{"path": "a.txt"})
	if _, err := run(t, sub, "edit_file", map[string]any{
		"path": "a.txt", "old_string": "старое", "new_string": "новое",
	}); err == nil {
		t.Error("субагент получил право редактировать файл, который не читал")
	}
	// Прочитав сам, субагент может править.
	mustRun(t, sub, "read_file", map[string]any{"path": "a.txt"})
	if _, err := run(t, sub, "edit_file", map[string]any{
		"path": "a.txt", "old_string": "старое", "new_string": "новое",
	}); err != nil {
		t.Errorf("после собственного чтения edit_file вернул ошибку: %v", err)
	}
}

// TestChangesJournalStillShared — правки субагента обязаны попасть в
// журнал главного агента. Обратная сторона изоляции: журнал общий.
func TestChangesJournalStillShared(t *testing.T) {
	dir := t.TempDir()
	r := newTestReg(t, dir)
	sub := r.Base().Restrict(nil, nil)

	mustRun(t, sub, "write_file", map[string]any{"path": "sub.txt", "content": "из субагента"})

	res := mustRun(t, r, "changes", nil)
	if !strings.Contains(res.Text, "sub.txt") {
		t.Errorf("правка субагента не попала в журнал главного:\n%s", res.Text)
	}
}

// TestAllBuiltinToolsRebind — у каждого встроенного инструмента есть
// фабрика пересборки. Один забытый инструмент означает, что его состояние
// молча утекает в копии реестра, и это невозможно заметить в обзоре кода.
func TestAllBuiltinToolsRebind(t *testing.T) {
	r := newTestReg(t, t.TempDir())
	var missing []string
	for _, t := range r.tools {
		if t.ExtName != "" || t.MCPSrv != "" {
			continue
		}
		if t.rebind == nil {
			missing = append(missing, t.Def.Name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("встроенные инструменты без пересборки обработчика: %s",
			strings.Join(missing, ", "))
	}
}

// TestRebindKeepsToolSet — пересборка обработчика не должна терять или
// дублировать инструменты в копии.
func TestRebindKeepsToolSet(t *testing.T) {
	r := newTestReg(t, t.TempDir())
	before := len(r.tools)

	for name, copy := range map[string]*Registry{
		"Base":     r.Base(),
		"Restrict": r.Base().Restrict(nil, nil),
		"Merge":    r.Merge(r.Base()),
	} {
		if len(copy.tools) != before {
			t.Errorf("%s: %d инструментов вместо %d", name, len(copy.tools), before)
		}
		// Каждый инструмент копии должен вызываться и не падать.
		for _, tool := range copy.tools {
			if tool.Handler == nil {
				t.Errorf("%s: у инструмента %s нет обработчика", name, tool.Def.Name)
			}
		}
	}
}

// TestBindToIsIdempotent — повторная пересборка не ломает обработчик:
// реестр могут копировать несколько раз подряд.
func TestBindToIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	mustWrite(t, p, "содержимое\n")

	r := newTestReg(t, dir)
	first := r.Base()
	second := first.Base()

	res := mustRun(t, second, "read_file", map[string]any{"path": "a.txt"})
	if res.Error != "" {
		t.Errorf("второй Base сломал инструмент: %s", res.Error)
	}
	if !second.env.ReadFiles[p] {
		t.Error("второй Base читал не в свою карту")
	}
	if first.env.ReadFiles[p] {
		t.Error("второй Base писал в карту первого")
	}
}
