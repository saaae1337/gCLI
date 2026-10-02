package main

// Форк сессии: ветвление диалога от любой точки.
//
// Зачем: «а что, если пойти другим путём с этого места» — обычный сценарий
// работы с агентом. /resume возвращает только целую сессию; /fork копирует
// историю до выбранного сообщения в НОВУЮ сессию, и оба пути дальше живут
// независимо. Нынешняя сессия не трогается.
//
// Резать историю можно не где попало: между assistant с tool_calls и
// ответами инструментов рвать нельзя — провайдеры отвергают такой хвост.
// Поэтому /fork режет только в «чистых» границах (см. safeForkCut).

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gcli/core"
)

// safeForkCut — ближайший безопасный разрез истории не дальше n сообщений.
//
// Граница i чистая, когда prefix messages[:i]: (а) не заканчивается открытым
// tool_calls без ответов, (б) не заканчивается ответом инструмента без
// вопроса (рвать между инструментом и следующим шагом модели тоже можно,
// но ответ без контекста вызова провайдеры не примут — таких хвостов не
// оставляем). Возврат: индекс для реза, 0 — если безопасных точек нет.
func safeForkCut(msgs []core.Message, n int) int {
	if n > len(msgs) {
		n = len(msgs)
	}
	if n < 0 {
		n = 0
	}
	// open — сколько вызовов инструментов ждут ответов в prefix.
	open := 0
	clean := make([]bool, len(msgs)+1)
	clean[0] = true
	for i, m := range msgs {
		switch m.Role {
		case core.RoleAssistant:
			open += len(m.ToolCalls)
		case core.RoleTool:
			if open > 0 {
				open--
			}
		}
		// Резать сразу перед сообщением i+1 можно, только когда все
		// вызовы закрыты. Хвост из tool-ответов отсеется ниже: следующим
		// сообщением в форке был бы вопрос пользователя, а провайдеры
		// требуют после tool-ответа шаг модели.
		if open == 0 {
			clean[i+1] = true
		}
	}
	for i := n; i >= 0; i-- {
		if !clean[i] {
			continue
		}
		if i > 0 && msgs[i-1].Role == core.RoleTool {
			continue
		}
		return i
	}
	return 0
}

// cmdFork — /fork [номер сообщения]: новая сессия из истории текущей.
func (a *app) cmdFork(rest string) {
	// Копия истории под sessMu: /fork режет историю на месте, а в serve-режиме
	// ход идёт параллельно и дописывает сообщения.
	msgs := a.Messages()
	n := len(msgs)
	if s := strings.TrimSpace(rest); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 1 {
			a.ui.Warn("укажи номер сообщения: /fork 12 (1 — первое сообщение, без числа — вся история)")
			return
		}
		n = v
	}
	if len(msgs) == 0 {
		a.ui.Info("сессия пуста — форковать нечего")
		return
	}
	cut := safeForkCut(msgs, n)
	if cut == 0 {
		a.ui.Warn("в этом месте истории резать нельзя (открытые вызовы инструментов) — возьми другой номер")
		return
	}
	cp := *a.sess
	cp.ID = time.Now().Format("20060102-150405") + "-" + core.RandID(4)
	cp.Title = "fork: " + core.Truncate(core.OneLine(a.sess.Title), 46)
	cp.Created = time.Now()
	cp.Updated = time.Now()
	cp.Messages = append([]core.Message(nil), msgs[:cut]...)
	cp.Todos = append([]core.Todo(nil), a.sess.Todos...)
	cp.Turns = 0
	cp.Usage = core.Usage{}
	cp.Stats = core.Stats{}
	cp.Checkpoints = nil
	cp.SubagentRuns = nil
	// Разрешения — свежие: это другой путь работы, доверия наследовать
	// не должно быть по умолчанию.
	cp.Perms = core.Perms{BashExact: map[string]bool{}}
	if err := a.repo.SaveSession(&cp); err != nil {
		a.ui.Err("форк не сохранился: " + err.Error())
		return
	}
	a.sess = &cp
	a.saveConfig()
	a.ui.Ok(fmt.Sprintf("форк создан: сессия %s, перенесено сообщений — %d из %d", cp.ID, cut, len(msgs)))
	a.ui.Info("нынешняя сессия переключена на форк; старая осталась в /sessions")
}
