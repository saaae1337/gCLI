package tools

import (
	"testing"
)

// builtinCount — число встроенных инструментов.
//
// Держим его явным, а не «посчитаем и напишем в README»: регистрация нового
// инструмента — это правка registerBuiltins, и без числа в тесте следующий
// человек посчитает руками, получит старое число и решит, что всё в порядке.
// Тест заставляет осознанно обновить и это число, и список в README.
//
// Считаются только встроенные (tools.New). skills, vision, субагенты, ext и
// MCP регистрируются поверх и зависят от настроек, поэтому в число не входят.
const builtinCount = 28

// TestBuiltinCount — столько инструментов должно быть в реестре по умолчанию.
func TestBuiltinCount(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	if got := r.Count(); got != builtinCount {
		t.Errorf("встроенных инструментов %d, ждали %d — новый инструмент добавлен без обновления числа?", got, builtinCount)
	}
}

// TestAllBuiltinsHaveSchemaAndHandler — у каждого инструмента должны быть
// описание, схема и обработчик. Без схемы модель не знает, как звать инструмент:
// он не появится в списке для провайдера, и вызов молча не сработает.
func TestAllBuiltinsHaveSchemaAndHandler(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	for _, tool := range r.All() {
		if tool.Handler == nil {
			t.Errorf("%s: обработчик nil", tool.Def.Name)
		}
		if tool.Def.Description == "" {
			t.Errorf("%s: пустое описание", tool.Def.Name)
		}
		if tool.Def.Schema == "" {
			t.Errorf("%s: пустая схема", tool.Def.Name)
		}
	}
}

// TestBuiltinNamesUnique — два инструмента с одинаковым именем означают, что
// модель зовёт один, а выполняется другой: по byName останется последний.
// Особенно опасно при копировании строки регистрации.
func TestBuiltinNamesUnique(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	seen := map[string]bool{}
	for _, name := range r.Names() {
		if seen[name] {
			t.Errorf("инструмент %s зарегистрирован дважды", name)
		}
		seen[name] = true
	}
}

// TestNewToolsRegistered — регрессия на инструменты из наборов handoff/changes/
// inspect/dry_run/job. Их удаление ломает не один вызов, а весь ход агента,
// поэтому список зафиксирован здесь явно.
func TestNewToolsRegistered(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	want := []string{
		"handoff", "handoff_read",
		"changes", "revert_last",
		"project_info", "inspect", "dry_run",
		"job", "ask_trace",
		"remember", "self_status", "verify",
		// Пакетный режим: пачка вместо N одиночных вызовов.
		"multi_read", "multi_edit", "multi_grep", "multi_bash",
	}
	for _, name := range want {
		if !r.Has(name) {
			t.Errorf("инструмент %s не зарегистрирован", name)
		}
	}
}
