package main

// Дашборд расходов: токены и деньги по всем сохранённым сессиям.
//
// Зачем: /usage показывает только текущую сессию, а человек, который гоняет
// миссии и субагентов каждый день, узнаёт про расходы из счёта провайдера —
// задним числом. Все данные уже лежат в sessions/*.json (Usage + Stats у
// каждой сессии), цены — в pricing.go; не хватало только витрины.
//
// Честность цифр: деньги считаются по локальной таблице цен и для кастомных
// endpoint-ов с неизвестной ценой равны нулю — это не «бесплатно», а «не знаем»,
// и в отчёте такие модели помечены явно.

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"gcli/core"
	"gcli/ui"
)

type statsDayRow struct {
	day      string
	sessions int
	requests int
	in       int
	out      int
	cost     float64
}

type statsModelRow struct {
	model    string
	provider string
	sessions int
	in       int
	out      int
	cost     float64
	known    bool
}

type statsTotals struct {
	sessions int
	requests int
	errors   int
	tools    int
	in       int
	out      int
	cost     float64
	unknown  int // сессий с моделями без известной цены
}

// statsAggregate — собрать и сгруппировать статистику по сессиям.
// Чистая функция: легко тестируется, ничего не печатает.
func statsAggregate(sessions []core.Session, since time.Time) (statsTotals, []statsDayRow, []statsModelRow) {
	var tot statsTotals
	days := map[string]*statsDayRow{}
	models := map[string]*statsModelRow{}
	for _, s := range sessions {
		if !s.Updated.IsZero() && s.Updated.Before(since) {
			continue
		}
		tot.sessions++
		tot.requests += s.Stats.Requests
		tot.errors += s.Stats.Errors
		tot.tools += s.Stats.Tools
		u := s.Usage
		tot.in += u.PromptTokens
		tot.out += u.CompletionTokens

		price, known := core.ModelPrice(s.Provider, s.Model)
		cost := price.Cost(u)
		tot.cost += cost
		if !known && u.Total() > 0 {
			tot.unknown++
		}

		day := s.Updated.Format("02.01")
		if d, ok := days[day]; ok {
			d.sessions++
			d.requests += s.Stats.Requests
			d.in += u.PromptTokens
			d.out += u.CompletionTokens
			d.cost += cost
		} else {
			days[day] = &statsDayRow{day: day, sessions: 1, requests: s.Stats.Requests,
				in: u.PromptTokens, out: u.CompletionTokens, cost: cost}
		}

		key := s.Provider + "/" + s.Model
		m, ok := models[key]
		if !ok {
			m = &statsModelRow{model: s.Model, provider: s.Provider, known: known}
			models[key] = m
		}
		m.sessions++
		m.in += u.PromptTokens
		m.out += u.CompletionTokens
		m.cost += cost
	}
	dayRows := make([]statsDayRow, 0, len(days))
	for _, d := range days {
		dayRows = append(dayRows, *d)
	}
	sort.Slice(dayRows, func(i, j int) bool { return dayRows[i].day > dayRows[j].day })
	modelRows := make([]statsModelRow, 0, len(models))
	for _, m := range models {
		modelRows = append(modelRows, *m)
	}
	sort.Slice(modelRows, func(i, j int) bool { return modelRows[i].cost > modelRows[j].cost })
	return tot, dayRows, modelRows
}

// cmdStats — /stats [дней]: расход по дням и моделям.
func (a *app) cmdStats(rest string) {
	days := 30
	if s := strings.TrimSpace(rest); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v >= 1 {
			days = v
		} else {
			a.ui.Warn("период задаётся числом дней: /stats 7")
			return
		}
	}
	a.printStats(a.repo.ListSessions(), days)
}

// printStats — печать дашборда. Отдельная от cmdStats, чтобы работал
// флаг -stats до входа в REPL.
func (a *app) printStats(sessions []core.Session, days int) {
	since := time.Now().AddDate(0, 0, -days+1).Truncate(24 * time.Hour)
	tot, dayRows, modelRows := statsAggregate(sessions, since)

	a.ui.Title("Расход за " + strconv.Itoa(days) + " дн.")
	a.ui.KVPairs([][2]string{
		{"сессий", strconv.Itoa(tot.sessions)},
		{"запросов", strconv.Itoa(tot.requests) + " (ошибок: " + strconv.Itoa(tot.errors) + ")"},
		{"токены", core.HumanSize(tot.in) + " вход / " + core.HumanSize(tot.out) + " выход"},
		{"деньги", core.FormatUSD(tot.cost)},
	})
	if tot.unknown > 0 {
		a.ui.Println("  " + a.ui.Gray("цена неизвестна для "+strconv.Itoa(tot.unknown)+" сессий с кастомными моделями — там $0 значит «не знаем», а не «бесплатно»"))
	}
	a.ui.Println("")

	if len(dayRows) == 0 {
		a.ui.Info("за период пусто — сессий не было")
		return
	}
	rows := make([][]string, 0, len(dayRows))
	for i, d := range dayRows {
		if i >= 15 {
			break
		}
		rows = append(rows, []string{
			d.day, strconv.Itoa(d.sessions), strconv.Itoa(d.requests),
			core.HumanSize(d.in + d.out), core.FormatUSD(d.cost),
		})
	}
	a.ui.Table([]ui.Column{
		{Title: "день", Width: 6}, {Title: "сессии", Width: 8, Right: true},
		{Title: "запросы", Width: 9, Right: true}, {Title: "токены", Width: 10, Right: true},
		{Title: "деньги", Width: 10, Right: true},
	}, rows, ui.BlockOpts{Title: "По дням"})

	if len(modelRows) > 0 {
		mrows := make([][]string, 0, len(modelRows))
		for i, m := range modelRows {
			if i >= 10 {
				break
			}
			cost := core.FormatUSD(m.cost)
			if !m.known {
				cost += "?"
			}
			mrows = append(mrows, []string{
				core.Truncate(m.provider+"/"+m.model, 34), strconv.Itoa(m.sessions),
				core.HumanSize(m.in + m.out), cost,
			})
		}
		a.ui.Table([]ui.Column{
			{Title: "модель", Width: 36}, {Title: "сессии", Width: 8, Right: true},
			{Title: "токены", Width: 10, Right: true}, {Title: "деньги", Width: 10, Right: true},
		}, mrows, ui.BlockOpts{Title: "По моделям"})
	}
	a.ui.Println("  " + a.ui.Gray("цены — локальная таблица gcli; для точного счёта сверяйся с провайдером"))
}
