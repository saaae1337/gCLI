package main

// Автонавыки: миссия, которая удалась, сама пишет навык.
//
// Зачем: удачный автономный прогон — это не только изменённые файлы, но и
// знание «как это делалось»: какие команды проверяли результат, где лежат
// нужные файлы, какие грабли встретились. Сейчас это знание умирает вместе
// с сессией. Навык в .gcli/skills/ переживает её, и следующий похожий прогон
// (или субагент через load_skill) начинает не с нуля. Замкнутый цикл опыта:
// gCLI учится на своих же миссиях — этого нет ни у OpenCode, ни у Claude Code.
//
// Честность: тело навыка строится детерминированно из журнала (цель, итоги,
// хроника чекпоинтов, частые инструменты) — без вызова модели. Никаких
// выдуманных «уроков»: только факты прогона. Выключается ключом
// mission_auto_skill: false в config.json.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"gcli/core"
)

// skillBodyMaxCheckpoints — сколько последних чекпоинтов уходит в навык.
const skillBodyMaxCheckpoints = 8

// missionSkillName — имя навыка по времени прогона. Имя из цели не строим:
// цель на русском, а валидное имя — [a-z0-9_-]; дата информативнее абракадабры.
func missionSkillName(t time.Time) string {
	return "auto-" + t.Format("0102-1504")
}

// topTools — самые частые инструменты прогона, до n.
func topTools(counts map[string]int, n int) []string {
	type kv struct {
		k string
		v int
	}
	list := make([]kv, 0, len(counts))
	for k, v := range counts {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].v != list[j].v {
			return list[i].v > list[j].v
		}
		return list[i].k < list[j].k
	})
	out := make([]string, 0, n)
	for i, x := range list {
		if i >= n {
			break
		}
		out = append(out, x.k)
	}
	return out
}

// toolCounts — частота инструментов по журналу прогона.
func toolCounts(recs []core.JournalRecord) map[string]int {
	m := map[string]int{}
	for _, r := range recs {
		for _, t := range r.Tools {
			m[t]++
		}
	}
	return m
}

// buildMissionSkill — собрать имя и содержимое навыка из данных прогона.
// Чистая функция: всё, что нужно, — в missionReportData.
func buildMissionSkill(d missionReportData, now time.Time) (string, string) {
	name := missionSkillName(now)
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", name)
	objective := core.Truncate(core.OneLine(d.Mission.Objective), 70)
	fmt.Fprintf(&b, "description: Опыт автономного прогона «%s» — как проверять и выполнять похожую работу в этом проекте\n", objective)
	fmt.Fprintf(&b, "when: %s\n", core.Truncate(core.OneLine(d.Mission.Objective), 80))
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# Опыт миссии: %s\n\n", objective)

	b.WriteString("Навык собран автоматически из журнала успешно завершённого прогона. Используй как справочник: чем проверять результат и где лежат точки входа.\n\n")

	if d.Tracker != nil {
		fmt.Fprintf(&b, "- Статус прогона: %s\n", d.Tracker.Status())
	}
	if d.HasState {
		fmt.Fprintf(&b, "- Счётчики: итераций %d, вызовов инструментов %d, токенов %d.\n",
			d.State.Iters, d.State.ToolCalls, d.State.Tokens)
	}

	// Чекпоинты — хроника «что делалось», от поздних к ранним: важнее
	// конец работы, там финальное состояние.
	var checkpoints []string
	for _, r := range d.Records {
		if r.Kind == "checkpoint" && strings.TrimSpace(r.Summary) != "" {
			checkpoints = append(checkpoints, r.Summary)
		}
	}
	if len(checkpoints) > 0 {
		b.WriteString("\n## Ход работы (от поздних чекпоинтов)\n\n")
		start := 0
		if len(checkpoints) > skillBodyMaxCheckpoints {
			start = len(checkpoints) - skillBodyMaxCheckpoints
		}
		for i := len(checkpoints) - 1; i >= start; i-- {
			fmt.Fprintf(&b, "- %s\n", core.Truncate(core.OneLine(checkpoints[i]), 160))
		}
	}

	if tools := topTools(toolCounts(d.Records), 6); len(tools) > 0 {
		b.WriteString("\n## Инструменты, на которых держалась работа\n\n")
		for _, t := range tools {
			fmt.Fprintf(&b, "- %s\n", t)
		}
	}

	b.WriteString("\n## Проверка результата\n\n")
	b.WriteString("Начни с тех же команд, которыми прогон проверял критерии приёмки (см. verify в журнале и /mission report).\n")
	return name, b.String()
}

// distillMissionSkill — записать навык успешной миссии. Вызывается из
// Stopped-колбэка при reason == StopDone; все ошибки молча пропускаются:
// обучающая петля не должна ломать завершение прогона.
func (a *app) distillMissionSkill() {
	if a.repo.Cfg.MissionAutoSkill != nil && !*a.repo.Cfg.MissionAutoSkill {
		return
	}
	recs, tailOK, err := core.ReadJournal(core.JournalPath(a.workDir))
	if err != nil || !tailOK {
		return // оборванный журнал — не материал для уроков
	}
	st, hasState, _ := core.LoadMissionState(core.MissionStatePath(a.workDir))
	d := missionReportData{
		Mission:  a.mission,
		Tracker:  a.missionTr,
		State:    st,
		HasState: hasState,
		Records:  recs,
		TailOK:   tailOK,
	}
	name, content := buildMissionSkill(d, time.Now())
	// Проектные навыки живут в .gcli/skills проекта — у этого опыта
	// ценность только внутри проекта. WriteAtomic сам создаст каталог.
	path := a.workDir + "/.gcli/skills/" + name + ".md"
	if err := core.WriteAtomic(path, []byte(content), 0o644); err != nil {
		return
	}
	if !a.quiet {
		a.ui.Info("опыт миссии записан в навык: " + path + " (load_skill \"" + name + "\")")
	}
}
