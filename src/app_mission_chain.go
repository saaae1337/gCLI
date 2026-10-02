package main

// Команды цепочки миссий: /mission chain add|list|clear|run.
//
// Драйвер — chainRun: запускает шаги по очереди, каждый шаг — обычный
// ход a.turn со своим трекером миссии. Цикл живёт в REPL-команде:
// ход всё равно блокирует REPL, а прерывание (Ctrl+C → ошибка хода)
// естественно останавливает и цепочку. Файл цепочки остаётся на диске
// с актуальным Index — после перезапуска run продолжит с места останова.

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gcli/core"
	"gcli/ui"
)

// missionChainAdd — /mission chain add <режим> <срок> <бюджет> <цель>.
func (a *app) missionChainAdd(args string) {
	m, err := parseMissionArgs(args)
	if err != nil {
		a.ui.Err("цепочка: " + err.Error())
		return
	}
	if m.Objective == "" {
		a.ui.Err("цепочка: у шага должна быть цель — /mission chain add long-time «починить пакет core»")
		return
	}
	m = m.Apply()
	// Шаг без потолков — это вечный прогон: цепочка такого не прощает.
	if !m.Long() && !m.Budgeted() && m.Deadline <= 0 && m.MaxIters <= 0 {
		a.ui.Err("цепочка: шаг должен иметь потолок (режим, срок или бюджет), иначе шаг никогда не кончится")
		return
	}
	a.missionChain.Items = append(a.missionChain.Items, m)
	if err := a.missionChain.Save(a.workDir); err != nil {
		a.ui.Err("цепочка не сохранена: " + err.Error())
		return
	}
	a.ui.Ok(fmt.Sprintf("шаг %d добавлен: %s", len(a.missionChain.Items), m.Summary()))
}

// missionChainList — /mission chain list.
func (a *app) missionChainList() {
	if len(a.missionChain.Items) == 0 {
		a.ui.Info("цепочка пуста — /mission chain add long-time «цель шага»")
		return
	}
	rows := make([][]string, 0, len(a.missionChain.Items))
	for i, m := range a.missionChain.Items {
		state := "—"
		switch {
		case i < a.missionChain.Index:
			state = "готово"
		case i == a.missionChain.Index:
			state = "следующий"
		}
		rows = append(rows, []string{
			strconv.Itoa(i + 1), state, core.Truncate(core.OneLine(m.Objective), 44),
			string(m.Mode), m.Summary(),
		})
	}
	a.ui.Table([]ui.Column{
		{Title: "#", Width: 3, Right: true}, {Title: "статус", Width: 10},
		{Title: "цель", Width: 46}, {Title: "режим", Width: 16}, {Title: "потолки", Width: 22},
	}, rows, ui.BlockOpts{Title: "Цепочка миссий — " + a.missionChain.Summary()})
	a.ui.Println("  " + a.ui.Gray("запустить/продолжить: /mission chain run"))
}

// chainStepPrompt — промпт очередного шага с переносом контекста.
func chainStepPrompt(c *core.MissionChain, m core.Mission) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Цепочка миссий: шаг %d из %d.\n", c.Index+1, len(c.Items))
	b.WriteString("Цель этого шага: " + m.Objective + "\n")
	if len(m.Acceptance) > 0 {
		b.WriteString("Критерии приёмки шага:\n")
		for _, ac := range m.Acceptance {
			b.WriteString("- " + ac + "\n")
		}
	}
	if c.Index > 0 {
		prev := c.Items[c.Index-1]
		b.WriteString("Предыдущий шаг («" + core.OneLine(prev.Objective) + "») только что завершился: его результат уже в проекте. Начни с проверки фактического состояния кода, а не с предположений.\n")
	}
	b.WriteString("Работай в обычном автономном режиме: сам крути цикл до критериев приёмки или потолка шага.")
	return b.String()
}

// chainRun — /mission chain run: исполнить шаги с текущего места.
func (a *app) chainRun() {
	if a.missionChain == nil {
		a.missionChain = &core.MissionChain{}
	}
	if len(a.missionChain.Items) == 0 {
		a.ui.Warn("цепочка пуста — сначала /mission chain add <режим> <цель>")
		return
	}
	if a.missionChain.Done() {
		a.ui.Info("все шаги цепочки уже отработали — добавьте новые (/mission chain add)")
		return
	}
	for !a.missionChain.Done() {
		next, ok := a.missionChain.Next()
		if !ok {
			break
		}
		a.missionChain.Index++
		if err := a.missionChain.Save(a.workDir); err != nil {
			a.ui.Err("цепочка не сохранилась: " + err.Error())
			return
		}
		a.mission = next.Apply()
		if !a.startTracker() {
			a.ui.Warn("шаг " + strconv.Itoa(a.missionChain.Index) + " не задаёт потолков — пропущен")
			continue
		}
		if err := a.saveMissionState("цепочка: шаг " + strconv.Itoa(a.missionChain.Index)); err != nil {
			a.ui.Warn("состояние прогона не сохранено: " + err.Error())
		}
		a.ui.Info(fmt.Sprintf("цепочка: шаг %d/%d запущен — %s",
			a.missionChain.Index, len(a.missionChain.Items), next.Summary()))
		if err := a.turn(chainStepPrompt(a.missionChain, next)); err != nil {
			a.ui.Err("цепочка прервана на шаге " + strconv.Itoa(a.missionChain.Index) + ": " + err.Error())
			a.ui.Info("прогресс сохранён — продолжение: /mission chain run")
			return
		}
		a.missionJournalStop("chain_step_done")
	}
	a.ui.Ok("цепочка завершена: все шаги отработали")
	// Цепочка исполнена — файл больше не нужен, чтобы после перезапуска
	// run не начинал всё заново.
	if a.missionChain.Done() {
		_ = os.Remove(core.MissionChainPath(a.workDir))
		a.missionChain = &core.MissionChain{}
	}
}

// missionChainEnsure — ленивая загрузка цепочки с диска: один раз за сеанс.
func (a *app) missionChainEnsure() {
	if a.missionChain != nil {
		return
	}
	c, ok, err := core.LoadMissionChain(a.workDir)
	if err != nil {
		a.ui.Warn("цепочка: " + err.Error() + " — начинаю с пустой")
	}
	if ok && c != nil {
		a.missionChain = c
		return
	}
	a.missionChain = &core.MissionChain{}
}

// cmdMissionChain — диспетчер /mission chain <подкоманда>.
func (a *app) cmdMissionChain(rest string) {
	a.missionChainEnsure()
	sub, args := splitFirstWord(rest)
	switch sub {
	case "":
		a.missionChainList()
	case "add":
		a.missionChainAdd(args)
	case "list":
		a.missionChainList()
	case "clear", "reset":
		a.missionChain = &core.MissionChain{}
		_ = os.Remove(core.MissionChainPath(a.workDir))
		a.ui.Info("цепочка очищена")
	case "run":
		a.chainRun()
	default:
		a.ui.Warn("не знаю подкоманду «" + sub + "»: add | list | clear | run")
	}
}
