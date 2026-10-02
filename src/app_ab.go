package main

// A/B моделей: один промпт — две модели параллельно, ответы рядом.
//
// Зачем: выбор модели обычно решается по чужим обзорам. /ab даёт решить
// по своему делу за один вызов: тот же вопрос ушли обеим, сравнили текст,
// время и цену. Дешевле, чем две сессии, и честнее, чем «помню, как было
// в прошлый раз».

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"gcli/core"
	"gcli/providers"
	"gcli/ui"
)

type abResult struct {
	model    string
	text     string
	err      error
	in       int
	out      int
	duration time.Duration
}

// parseAbArgs — «модель промпт...»: модель опциональна, но без неё сравнивать
// не с чем. Модель — первое слово, если промпт длиннее одного слова; иначе
// это просто промпт без второй стороны.
func parseAbArgs(rest string) (model, prompt string, err error) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", "", fmt.Errorf("укажи модель и промпт: /ab glm-4.5-air расскажи, чем хорош Go")
	}
	word, tail, ok := strings.Cut(rest, " ")
	if !ok || strings.TrimSpace(tail) == "" {
		return "", "", fmt.Errorf("промпт пустой: после модели напиши вопрос — /ab %s <вопрос>", word)
	}
	return word, strings.TrimSpace(tail), nil
}

// abAsk — один запрос без инструментов: чистый текст, usage, время.
// Канал закрываем после Stream — как в agent.callModel: иначе читатель
// зависает навсегда при ранней ошибке (нет ключа, отказ сети).
func (a *app) abAsk(ctx context.Context, model, prompt string) abResult {
	start := time.Now()
	res := abResult{model: model}
	out := make(chan core.Delta, 512)
	done := make(chan error, 1)
	creq := core.ChatRequest{
		Model:     model,
		Messages:  []core.Message{{Role: core.RoleUser, Content: prompt}},
		MaxTokens: 8192,
	}
	go func() {
		defer close(out)
		done <- providers.Stream(ctx, a.client, a.prov, creq, providers.ThinkState(a.repo.Cfg), out)
	}()
	for d := range out {
		if d.Usage != nil {
			res.in += d.Usage.PromptTokens
			res.out += d.Usage.CompletionTokens
		}
		res.text += d.Text
	}
	res.err = <-done
	res.duration = time.Since(start)
	return res
}

// cmdAb — /ab <модель> <промпт>: текущая модель против указанной.
func (a *app) cmdAb(rest string) {
	modelB, prompt, err := parseAbArgs(rest)
	if err != nil {
		a.ui.Warn(err.Error())
		return
	}
	modelA := a.model

	a.ui.Info("жду оба ответа: " + modelA + " и " + modelB)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var ra, rb abResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); ra = a.abAsk(ctx, modelA, prompt) }()
	go func() { defer wg.Done(); rb = a.abAsk(ctx, modelB, prompt) }()
	wg.Wait()

	for _, r := range []abResult{ra, rb} {
		title := "A · " + r.model
		if r.model == modelB {
			title = "B · " + r.model
		}
		if r.err != nil {
			a.ui.Block("ошибка: "+r.err.Error(), ui.BlockOpts{Title: title})
			continue
		}
		a.ui.Block(r.text, ui.BlockOpts{Title: title})
		price, _ := core.ModelPrice(a.prov.ID, r.model)
		a.ui.KVPairs([][2]string{
			{title, fmt.Sprintf("%s · %d/%d ткн · %s",
				r.duration.Round(time.Millisecond), r.in, r.out,
				core.FormatUSD(price.Cost(core.Usage{PromptTokens: r.in, CompletionTokens: r.out})))},
		})
	}
	a.ui.Println("  " + a.ui.Gray("оба ответа в контекст не попадают: /ab — витрина, не диалог"))
}
