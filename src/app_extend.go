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
func (a *app) cmdIters() {
	base, abs, maxExtends, step := a.extendLimits()
	a.ui.Println("")
	a.ui.KVPairs([][2]string{
		{"Лимит хода", strconv.Itoa(base) + " итераций"},
		{"Продление", "+" + strconv.Itoa(step) + " за раз · не чаще " +
			strconv.Itoa(maxExtends) + " раз за ход"},
		{"Потолок", strconv.Itoa(abs) + " итераций (с учётом продлений)"},
	})

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
