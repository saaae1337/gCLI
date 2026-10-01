package main

import (
	"fmt"
	"strconv"

	"gcli/agent"
	"gcli/core"
)

// ---------- Продление хода: мост между инструментом и агентом ----------
//
// Инструмент extend_turns не знает, кто ведёт ход, — он знает только
// Env.Extend. Связка живёт здесь по двум причинам.
//
// Первая — безопасность. Инструмент зовётся из горутины инструментов агента,
// а состояние продлений принадлежит ходу. Между вызовом инструмента и чтением
// a.lastAgent ход может закончиться, и тогда lastAgent указывал бы уже на
// следующий. Поэтому nil-ответ — это не ошибка, а «продлять некого».
//
// Вторая — наблюдаемость. Продление меняет лимит хода на ходу: человек
// должен видеть это в интерфейсе, иначе тихий рост числа итераций выглядит
// как зависание.

// extendTurn — обработчик Env.Extend: спросить у агента продление.
func (a *app) extendTurn(reason string, n int) (int, string, error) {
	ag := a.currentAgent()
	if ag == nil {
		return 0, "", fmt.Errorf("продление недоступно: агент ещё не начал ход")
	}
	v := ag.ExtendTurn(reason, n)
	if v.OK {
		return v.Granted, v.Message, nil
	}
	// Отказ — не исключение инструмента: модель должна прочитать причину и
	// решить, что делать дальше, а не упасть с ошибкой вызова.
	return 0, v.Message, nil
}

// onExtend — показать пользователю, что лимит хода вырос.
//
// Сообщение идёт через OnNote по одной причине: оно уже попадает в промпт
// следующего запроса агента, и пользователь видит ту же строку в списке
// заметок. Отдельный канал означал бы два источника правды об одном факте.
func (a *app) onExtend(granted, total int, reason string) {
	if granted <= 0 {
		return
	}
	a.onNote("продление хода", fmt.Sprintf("+%d итераций (новый лимит %d): %s",
		granted, total, core.Truncate(core.OneLine(reason), 160)))
}

// extendLimits — текстовое описание лимитов продления для команды /iters.
func (a *app) extendLimits() (base, abs, maxExtends, step int) {
	base = a.maxIters()
	abs = a.maxItersAbs()
	maxExtends = a.turnExtendMax()
	step = a.turnExtendStep()
	if maxExtends <= 0 {
		maxExtends = agent.DefaultExtendMax
	}
	if step <= 0 {
		step = agent.DefaultExtendStep
	}
	return
}

// cmdIters — /iters: показать бюджет итераций хода и журнал продлений.
//
// Команда отвечает на вопрос, который иначе приходилось угадывать по
// обрыву работы: «сколько ещё можно» и «продлевал ли уже агент ход».
// Показываем и лимиты из конфига, и живое состояние текущего хода —
// расходятся они только во время хода, и именно тогда разница и важна.
//
// Когда запущен автономный прогон, рядом показываем и его расход: у
// прогона свои потолки (время, токены, деньги), и лимит хода про них
// ничего не знает. Раньше /iters молчал о прогоне — на многочасовом
// запуске человек смотрел не туда.
func (a *app) cmdIters() {
	base, abs, maxExtends, step := a.extendLimits()
	a.ui.Println("")
	a.ui.KVPairs([][2]string{
		{"Лимит хода", strconv.Itoa(base) + " итераций"},
		{"Продление", "+" + strconv.Itoa(step) + " за раз · не чаще " +
			strconv.Itoa(maxExtends) + " раз за ход"},
		{"Потолок", strconv.Itoa(abs) + " итераций (с учётом продлений)"},
	})

	a.printMissionIters()

	ag := a.currentAgent()
	if ag == nil || ag.ExtendState() == nil {
		a.ui.Println("")
		a.ui.Println(a.ui.Gray("Ход не идёт — журнал продлений пуст."))
		a.ui.Println("")
		return
	}

	st := ag.ExtendState()
	a.ui.Println("")
	a.ui.Println(a.ui.Gray(fmt.Sprintf("Текущий ход: %d из %d итераций, продлений %d",
		ag.Turns, st.Limit(), st.Extends())))
	if log := ag.ExtendLog(); len(log) > 0 {
		a.ui.Println("")
		a.ui.Println(a.ui.Bold("Журнал решений:"))
		for _, line := range log {
			a.ui.Println("  " + core.Truncate(core.OneLine(line), 150))
		}
	}
	a.ui.Println("")
}

// printMissionIters — блок прогона в /iters: расход потолков миссии.
//
// Отдельная функция, потому что расчёты здесь не про ход: ход —
// это итерации от extendLimits, прогон — время/токены/деньги из
// трекера миссии. Смешивать их в одной выдаче значит снова
// получить «смотрю не туда».
func (a *app) printMissionIters() {
	tr := a.missionTr
	if tr == nil {
		return
	}
	m := tr.Mission()

	a.ui.Println("")
	a.ui.Println(a.ui.Bold("Автономный прогон:"))
	rows := [][2]string{}
	if m.Mode != "" && m.Mode != core.MissionNormal {
		rows = append(rows, [2]string{"Режим", string(m.Mode)})
	}
	if m.Deadline > 0 {
		spent := core.FormatDur(tr.Elapsed())
		if left := tr.Left(); left < 0 {
			rows = append(rows, [2]string{"Время",
				spent + " · просрочено на " + core.FormatDur(-left)})
		} else {
			rows = append(rows, [2]string{"Время",
				spent + " · осталось " + core.FormatDur(left)})
		}
	} else {
		rows = append(rows, [2]string{"Время", core.FormatDur(tr.Elapsed())})
	}
	if m.MaxIters > 0 {
		rows = append(rows, [2]string{"Итерации",
			strconv.Itoa(tr.Iters()) + " из " + strconv.Itoa(m.MaxIters)})
	} else {
		rows = append(rows, [2]string{"Итерации", strconv.Itoa(tr.Iters())})
	}
	rows = append(rows, [2]string{"Вызовы инструментов", strconv.Itoa(tr.ToolCalls())})
	if m.TokenBudget > 0 {
		rows = append(rows, [2]string{"Токены",
			core.CompactNum(tr.Spent()) + " из " + core.CompactNum(m.TokenBudget)})
	} else {
		rows = append(rows, [2]string{"Токены", core.CompactNum(tr.Spent())})
	}
	if cost := tr.Cost(a.prov.ID, a.model); cost > 0 || m.CostBudget > 0 {
		c := "$" + strconv.FormatFloat(cost, 'f', 2, 64)
		if m.CostBudget > 0 {
			c += " из $" + strconv.FormatFloat(m.CostBudget, 'f', 2, 64)
		}
		rows = append(rows, [2]string{"Стоимость", c})
	}
	if cp := tr.Checkpoints(); cp > 0 {
		rows = append(rows, [2]string{"Чекпоинты", strconv.Itoa(cp)})
	}
	if cont := tr.Continues(); cont > 0 {
		rows = append(rows, [2]string{"Продолжений после «готово»", strconv.Itoa(cont)})
	}
	if done := tr.Stopped(); done != "" {
		rows = append(rows, [2]string{"Статус",
			"остановлен: " + core.StopReasonLabel(done)})
	} else {
		rows = append(rows, [2]string{"Статус", tr.Status()})
	}
	a.ui.KVPairs(rows)
	if obj := core.OneLine(m.Objective); obj != "" {
		a.ui.Println(a.ui.Gray("  цель: " + core.Truncate(obj, 120)))
	}
}
