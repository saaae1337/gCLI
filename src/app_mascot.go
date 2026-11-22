package main

import (
	"strings"
	"time"

	"gcli/ui"
)

// ---------- Маскот Искра ----------
//
// Сама команда /mascot убрана (итерация 1): Искра — часть интерфейса,
// а не настройка в списке команд. Включение/выключение — config.json
// ("mascot") или переменная окружения GCLI_MASCOT=0.

// mascotPurr — реплики при успешном результате.
var mascotPurr = []string{
	"готово! мурррр",
	"получилось — заслужила рыбку",
	"мур-мур, всё сделала",
	"задача решена, хвост трубой",
}

// mascotOopsLines — реплики при ошибке хода.
var mascotOopsLines = []string{
	"мяу! что-то не так",
	"шерсть дыбом — ошибка",
	"фырк! надо разобраться",
	"не получилось... погладь и повтори",
}

// mascotAngryLines — реплики при повторной ошибке подряд.
var mascotAngryLines = []string{
	"опять?! шершавый день",
	"два раза подряд — нюхаю, где тут сломалось",
	"мяу! так дела не делаются",
}

// mascotFarewells — прощания при выходе.
var mascotFarewells = []string{
	"свернулась калачиком — до связи!",
	"zZ... иду спать. мяу",
	"хвост на прощание помахал — пока!",
}

// setMascot — включить/выключить маскота и сохранить настройку.
func (a *app) setMascot(on bool) {
	cfgMu.Lock()
	a.repo.Cfg.Mascot = &on
	cfgMu.Unlock()
	a.saveConfig()
	a.applyMascot()
	// Заставка TUI — единственное место, где кот виден в полноэкранном
	// режиме; переключаем её сразу, не дожидаясь перезапуска.
	if t := a.tuiApp(); t != nil {
		t.SetMascot(on)
	}
}

// mascotEnabled — итоговое решение «показывать ли маскота».
//
// Приоритет: переменная окружения GCLI_MASCOT (0/off — принудительно
// выключить) → config.json ("mascot", nil = включён) → по умолчанию вкл.
func mascotEnabled(cfgVal *bool, env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "0", "off", "false", "no", "выкл":
		return false
	case "1", "on", "true", "yes", "вкл":
		return true
	}
	if cfgVal != nil {
		return *cfgVal
	}
	return true
}

// applyMascot — применить решение о маскоте к интерфейсу.
func (a *app) applyMascot() {
	a.ui.SetMascot(mascotEnabled(a.repo.Cfg.Mascot, osMascotEnv()))
}

// mascotBye — маскот прощается при выходе: сворачивается калачиком.
func (a *app) mascotBye() {
	if !a.ui.Mascot() {
		return
	}
	a.ui.MascotSayLine(ui.MascotSleep, mascotFarewells[time.Now().UnixNano()%int64(len(mascotFarewells))])
}

// mascotResult — реплика по итогам хода: мурчание с подмигиванием
// при успехе, фырканье при ошибке, а после двух ошибок подряд —
// сердитое «опять?!».
func (a *app) mascotResult(ok bool) {
	if !a.ui.Mascot() {
		return
	}
	if ok {
		// Иногда вместо мурчания — подмиг: живость без лишнего шума.
		if time.Now().UnixNano()%3 == 0 {
			a.ui.MascotSayLine(ui.MascotWink, "мурр... видишь, как надо")
			return
		}
		a.ui.MascotSayLine(ui.MascotHappy,
			mascotPurr[time.Now().UnixNano()%int64(len(mascotPurr))])
		return
	}
	if a.lastTurnFailed {
		a.ui.MascotSayLine(ui.MascotAngry,
			mascotAngryLines[time.Now().UnixNano()%int64(len(mascotAngryLines))])
		return
	}
	a.ui.MascotSayLine(ui.MascotOops,
		mascotOopsLines[time.Now().UnixNano()%int64(len(mascotOopsLines))])
}

// ---------- Команда /plan ----------

// cmdPlan — режим планирования в духе Claude Code.
//
// В этом режиме агент сначала исследует задачу (чтение, поиск — можно),
// составляет план и ждёт одобрения; файлы не меняет и не запускает
// опасные команды, пока пользователь не скажет «ок» / «делай».
func (a *app) cmdPlan(arg string) {
	cfgMu.Lock()
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "on", "вкл":
		a.repo.Cfg.PlanMode = true
	case "off", "выкл":
		a.repo.Cfg.PlanMode = false
	default:
		a.repo.Cfg.PlanMode = !a.repo.Cfg.PlanMode
	}
	cfgMu.Unlock()
	a.saveConfig()
	if a.repo.Cfg.PlanMode {
		a.ui.Ok("режим планирования: агент сначала предложит план (/plan off — выключить)")
		a.ui.Hint("он исследует задачу и не изменит файлы, пока ты не одобришь план")
		return
	}
	a.ui.Ok("обычный режим: агент сразу действует")
}

// planModeSystem — надстройка к системному промпту для /plan.
const planModeSystem = "" +
	"## Режим планирования (plan mode)\n" +
	"Ты работаешь в режиме планирования. Правила:\n" +
	"1. Разрешены только «читающие» инструменты: read_file, list_dir, glob, grep, web_search, web_fetch, think.\n" +
	"2. Запрещено менять файлы (write_file, edit_file), запускать bash и делегировать субагентам — " +
	"даже если задача явно просит: сначала план.\n" +
	"3. Изучи контекст, затем выдай план: цель, шаги по порядку, файлы, которые затронешь, " +
	"риски и открытые вопросы.\n" +
	"4. Заверши ответ фразой «Одобри план (ок) — приступлю». Не начинай реализацию, пока " +
	"пользователь не одобрит план явным «ок», «делай», «согласен» или своим текстом-подтверждением.\n" +
	"5. Если пользователь одобрил план (следующее сообщение после твоего плана) — работай в обычном режиме."
