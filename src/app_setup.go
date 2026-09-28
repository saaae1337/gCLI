package main

import (
	"fmt"
	"strconv"
	"strings"

	"gcli/core"
	"gcli/providers"
	"gcli/tools"
	"gcli/ui"
)

// runSetup — мастер настройки: провайдер → ключ → модель.
func (a *app) runSetup() {
	a.ui.Println("")
	a.ui.Println("  " + a.ui.Bold("Мастер настройки gcli") + a.ui.Gray("   провайдер → ключ → модель"))
	a.listProviders()

	// 1. Провайдер.
	a.ui.Prompt2("Провайдер (номер или id, Enter — "+a.prov.ID+")", "  ")
	if !a.stdin.Scan() {
		return
	}
	p := a.prov
	if s := strings.TrimSpace(a.stdin.Text()); s != "" {
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
		a.ui.Prompt2("API-ключ для "+p.ID+cur, "  ("+hint+") ")
		if !a.stdin.Scan() {
			return
		}
		if k := providers.CleanKey(a.stdin.Text()); k != "" {
			a.repo.EnsureProviderCfg(p.ID).APIKey = k
			a.saveConfig()
			a.registry = providers.Build(a.repo.Cfg)
			if np := a.registry.Find(p.ID); np != nil {
				p = np
			}
		}
	}

	// 3. Модель — из живого списка.
	a.ui.SpinnerStart("Получаю список моделей с " + p.ID)
	models, err := providers.FetchModels(nil, a.client, p)
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
		a.ui.Prompt2("Модель (номер или имя)", "  ")
		if !a.stdin.Scan() {
			return
		}
		s := strings.TrimSpace(a.stdin.Text())
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
		a.ui.Prompt2("Модель вручную (Enter — "+p.DefaultModel+")", "  ")
		if !a.stdin.Scan() {
			return
		}
		s := strings.TrimSpace(a.stdin.Text())
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
	a.repo.Cfg.Provider = p.ID
	a.repo.Cfg.Model = model
	a.saveConfig()
	a.registry = providers.Build(a.repo.Cfg)
	if np := a.registry.Find(p.ID); np != nil {
		a.prov = np
	}
	a.model = model
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

// confirmExtWarn — предупреждение о расширениях при старте (вызывается из main).
func (a *app) extWarnings() []string {
	warns := a.tools.RegisterExtTools()
	if len(warns) > 0 && !a.quiet {
		for _, w := range warns {
			a.ui.Warn(w)
		}
	}
	return warns
}

var _ = fmt.Sprintf
var _ = tools.ValidSkillName
