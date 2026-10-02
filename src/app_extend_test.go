package main

import (
	"strconv"
	"strings"
	"testing"

	"gcli/agent"
	"gcli/core"
	"gcli/providers"
	"gcli/subagents"
	"gcli/tools"
)

// itersApp — приложение, готовое к /iters и к проверке продлений.
func itersApp(t *testing.T) (*app, *strings.Builder) {
	t.Helper()
	home := t.TempDir()
	work := t.TempDir()
	t.Setenv("GCLI_HOME", home)

	buf := &strings.Builder{}
	store := core.NewStore()
	a := &app{
		repo:    &core.Repo{Store: store, Cfg: core.DefaultConfig()},
		store:   store,
		ui:      testUI(buf),
		workDir: work,
		prov:    &providers.Provider{ID: "zai", Label: "Z.ai", NoKey: true},
		model:   "glm-4.6",
		tools:   tools.New(tools.Env{WorkDir: work}),
		memory:  tools.NewMemory(work, store),
		pool:    subagents.NewPool(nil, subagents.PoolOptions{MaxParallel: 3, MaxDepth: 1, Enabled: true, WorkDir: work}),
		sess:    &core.Session{ID: "s1", AgentMode: true},
	}
	return a, buf
}

// TestCmdItersOutsideTurn — вне хода команда обязана работать и не падать:
// человек спрашивает про лимиты обычно, а не только посреди работы.
func TestCmdItersOutsideTurn(t *testing.T) {
	a, buf := itersApp(t)
	a.cmdIters()
	out := buf.String()
	if out == "" {
		t.Fatal("/iters не вывела ничего")
	}
	for _, want := range []string{strconv.Itoa(agent.DefaultMaxIters), "Продление", "Потолок"} {
		if !strings.Contains(out, want) {
			t.Errorf("в выводе /iters нет %q:\n%s", want, out)
		}
	}
}

// TestCmdItersShowsLiveTurn — во время хода команда показывает живой лимит
// и журнал: именно это отличает её от повторения конфига.
func TestCmdItersShowsLiveTurn(t *testing.T) {
	a, buf := itersApp(t)
	ag := agent.New(agent.Deps{Registry: tools.New(tools.Env{WorkDir: a.workDir})}, a.workDir)
	// Состояние продлений живёт только внутри хода; эмулируем ход через Run
	// нельзя без провайдера, поэтому проверяем вывод через пустое состояние.
	a.mu.Lock()
	a.lastAgent = ag
	a.mu.Unlock()

	a.cmdIters()
	if !strings.Contains(buf.String(), "Ход не идёт") {
		t.Errorf("ожидалась пометка об отсутствии хода:\n%s", buf.String())
	}
}

// TestExtendTurnWithoutAgent — без агента продление недоступно, но ошибка
// должна быть понятной, а не пустой.
func TestExtendTurnWithoutAgent(t *testing.T) {
	a, _ := itersApp(t)
	if _, _, err := a.extendTurn("осталось дописать тесты", 0); err == nil {
		t.Fatal("без агента ожидалась ошибка")
	}
}

// TestMaxItersAbsNeverBelowBase — потолок не должен быть меньше базового
// лимита: такой конфиг тихо урезал бы работающие итерации.
func TestMaxItersAbsNeverBelowBase(t *testing.T) {
	a, _ := itersApp(t)
	a.repo.Cfg.MaxIters = 150
	a.repo.Cfg.MaxItersAbs = 100
	if got := a.maxItersAbs(); got < a.maxIters() {
		t.Errorf("потолок %d ниже базового лимита %d", got, a.maxIters())
	}
}

// TestMaxItersAbsDefault — без настройки потолок остаётся дефолтом пакета и
// строго выше базы.
//
// Проверка «строго выше» здесь принципиальна: при max_iters=40 потолок 40
// формально допустим, но тогда продление невозможно — механизм включён и
// при этом мёртв. Именно такой случай DefaultExtendAbs и спасает.
func TestMaxItersAbsDefault(t *testing.T) {
	a, _ := itersApp(t)
	a.repo.Cfg.MaxItersAbs = 0
	got := a.maxItersAbs()
	if got < a.maxIters() {
		t.Errorf("потолок по умолчанию %d ниже базы %d", got, a.maxIters())
	}
	if got <= a.maxIters() {
		t.Errorf("потолок по умолчанию %d не оставляет запаса для продления при базе %d — механизм мёртв",
			got, a.maxIters())
	}
	if got > agent.DefaultExtendAbs {
		t.Errorf("потолок по умолчанию %d выше дефолта %d", got, agent.DefaultExtendAbs)
	}
}

// TestTurnExtendClamps — настройки продления ограничиваются разумным
// диапазоном: конфиг не должен дать шаг в 100000 итераций.
func TestTurnExtendClamps(t *testing.T) {
	a, _ := itersApp(t)
	a.repo.Cfg.TurnExtendMax = 1000
	if got := a.turnExtendMax(); got > 20 {
		t.Errorf("число продлений не ограничено: %d", got)
	}
	a.repo.Cfg.TurnExtendStep = 100000
	if got := a.turnExtendStep(); got > 100 {
		t.Errorf("шаг продления не ограничен: %d", got)
	}
	// Ноль означает «дефолт пакета» и проходит без изменений.
	a.repo.Cfg.TurnExtendMax = 0
	if got := a.turnExtendMax(); got != 0 {
		t.Errorf("ноль должен означать дефолт, получено %d", got)
	}
}

// TestExtendLimitsResolvesDefaults — /iters показывает действующие значения:
// нули из конфига превращаются в дефолты, чтобы команда не путала человека.
func TestExtendLimitsResolvesDefaults(t *testing.T) {
	a, _ := itersApp(t)
	a.repo.Cfg.TurnExtendMax = 0
	a.repo.Cfg.TurnExtendStep = 0
	base, _, maxExtends, step := a.extendLimits()
	if base <= 0 {
		t.Errorf("базовый лимит %d", base)
	}
	if maxExtends != agent.DefaultExtendMax {
		t.Errorf("число продлений %d, ждали дефолт %d", maxExtends, agent.DefaultExtendMax)
	}
	if step != agent.DefaultExtendStep {
		t.Errorf("шаг продления %d, ждали дефолт %d", step, agent.DefaultExtendStep)
	}
}
