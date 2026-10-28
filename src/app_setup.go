package main

import (
	"context"
	"strconv"
	"strings"

	"gcli/core"
	"gcli/providers"
	"gcli/ui"
)

// runSetup — мастер настройки: провайдер → ключ → модель.
func (a *app) runSetup() {
	a.ui.Println("")
	a.ui.Println("  " + a.ui.Bold("Мастер настройки gcli") + a.ui.Gray("   провайдер → ключ → модель"))
	a.listProviders()

	// 1. Провайдер.
	s, ok := a.askLine("Провайдер (номер или id, Enter — " + a.prov.ID + ")")
	if !ok {
		return
	}
	p := a.prov
	if s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(a.registry.All) {
			p = a.registry.All[n-1]
		} else if cand := a.registry.Find(strings.ToLower(s)); cand != nil {
			p = cand
		} else {
			a.ui.Err("нет провайдера «" + s + "»")
			return
		}
	}

	// 2. Ключ.
	if p.NoKey {
		a.ui.Println("  " + a.ui.Gray("локальный провайдер — ключ не нужен ("+p.BaseURL+")"))
	} else {
		hint := "Enter — оставить как есть"
		if p.Key == "" {
			hint = "Enter — взять из переменных окружения"
		}
		cur := ""
		if p.Key != "" {
			cur = " (сейчас " + providers.MaskKey(p.Key) + ")"
		}
		k, ok := a.askSecret("API-ключ для " + p.ID + cur + " · " + hint)
		if !ok {
			return
		}
		if k = providers.CleanKey(k); k != "" {
			a.repo.EnsureProviderCfg(p.ID).APIKey = k
			a.saveConfig()
			a.registry = a.registry.Rebuild(a.repo.Cfg)
			if np := a.registry.Find(p.ID); np != nil {
				p = np
			}
		}
	}

	// 3. Модель — из живого списка.
	a.ui.SpinnerStart("Получаю список моделей с " + p.ID)
	models, err := providers.FetchModels(context.Background(), a.client, p)
	a.ui.SpinnerStop()

	model := ""
	if err == nil && len(models) > 0 {
		shown := core.Min(len(models), 20)
		rows := make([][]string, 0, shown)
		for i := 0; i < shown; i++ {
			cur := ""
			if models[i] == p.DefaultModel {
				cur = "← по умолчанию"
			}
			rows = append(rows, []string{strconv.Itoa(i + 1), models[i], cur})
		}
		a.ui.Table([]ui.Column{
			{Title: "", Width: 4, Right: true},
			{Title: "модель", Width: 44},
			{Title: "", Width: 18},
		}, rows, ui.BlockOpts{
			Title:  "Доступно моделей: " + strconv.Itoa(len(models)),
			Footer: "Enter — модель по умолчанию, или введи имя вручную",
		})
		s, ok := a.askLine("Модель (номер или имя)")
		if !ok {
			return
		}
		switch {
		case s == "":
			model = p.DefaultModel
		default:
			if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(models) {
				model = models[n-1]
			} else {
				model = s
			}
		}
	} else {
		if err != nil {
			a.ui.Warn("список моделей получить не удалось (" + core.Truncate(core.OneLine(err.Error()), 70) + ")")
		}
		s, ok := a.askLine("Модель вручную (Enter — " + p.DefaultModel + ")")
		if !ok {
			return
		}
		if s == "" {
			model = p.DefaultModel
		} else {
			model = s
		}
	}
	if model == "" {
		a.ui.Err("модель не выбрана")
		return
	}

	// Сохранение.
	pc := a.repo.EnsureProviderCfg(p.ID)
	pc.Model = model
	cfgMu.Lock()
	a.repo.Cfg.Provider = p.ID
	a.repo.Cfg.Model = model
	cfgMu.Unlock()
	a.saveConfig()
	a.registry = a.registry.Rebuild(a.repo.Cfg)
	if np := a.registry.Find(p.ID); np != nil {
		a.setProvider(np)
	}
	a.setModel(model)
	a.sess.Provider = a.prov.ID
	a.sess.Model = model
	a.saveSession()

	a.ui.Println("")
	a.ui.Block("Провайдер: "+a.prov.Label+"\nМодель:    "+model+"\nИнструментов: "+strconv.Itoa(a.tools.Count())+
		" · субагенты: "+enabledWord(a.pool.Enabled()),
		ui.BlockOpts{Title: "Готово", Accent: true})
	a.ui.Println("  " + a.ui.Gray("Ставь задачу — агент выполнит её, используя файлы, команды и веб."))
	a.ui.Println("")
}
