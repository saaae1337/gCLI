package main

import (
	"strings"
	"time"

	"gcli/ui"
)

// ---------- Команда /mascot ----------

// mascotGreetings — случайные приветствия Искры при запуске и /mascot.
var mascotGreetings = []string{
	"мяу! искра на посту — чем займёмся?",
	"выспалась, усы расчёсаны — готова помогать",
	"хвост трубой — давай что-нибудь соберём",
	"прыгнула на клавиатуру — жду задачу",
	"муррр. говори, что делать",
}

// mascotAccepted — реплики при приёме задачи (начало хода).
var mascotAccepted = []string{
	"мяу! беру задачу",
	"принято, шевелю усами",
	"задача у меня, думает голова",
	"так-так, лапки на клавиатуре",
}

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

// mascotStates — все состояния кота по референсу: порядок, подпись и
// реплика для /mascot demo. Первые шесть — основной жизненный цикл,
// остальные — настроение и пасхалки.
var mascotStates = []struct {
	s    ui.MascotState
	note string
}{
	{ui.MascotIdle, "ждёт команду"},
	{ui.MascotThink, "думает — модель рассуждает"},
	{ui.MascotWork, "работает — идут инструменты"},
	{ui.MascotHappy, "радуется — задача выполнена"},
	{ui.MascotOops, "хандрит — что-то сломалось"},
	{ui.MascotSleep, "спит — сессия закрыта"},
	{ui.MascotListen, "слушает — пользователь печатает"},
	{ui.MascotSurprised, "удивлён — неожиданный результат"},
	{ui.MascotBored, "скучает — долго нет команд"},
	{ui.MascotEat, "ест — обрабатывает большой объём"},
	{ui.MascotBlink, "моргает"},
	{ui.MascotWink, "подмигивает"},
	{ui.MascotContent, "доволен"},
	{ui.MascotShy, "смущён"},
	{ui.MascotCry, "плачет"},
	{ui.MascotAngry, "сердится"},
	{ui.MascotPensive, "задумчив"},
	{ui.MascotScared, "испуган"},
	{ui.MascotPurr, "мурлычет"},
}

// cmdMascot — /mascot [on|off|demo|say ...|wave].
func (a *app) cmdMascot(rest string) {
	parts := strings.Fields(rest)
	sub := ""
	if len(parts) > 0 {
		sub = strings.ToLower(parts[0])
	}
	switch sub {
	case "", "show", "показать":
		a.mascotShow(strings.Join(parts[minInt(1, len(parts)):], " "))
	case "on", "вкл":
		a.setMascot(true)
		a.ui.Ok("Искра оживает — встретит в баннере и будет сидеть над строкой состояния")
		a.ui.MascotSpeak(ui.MascotHappy, "я снова в деле! муррр")
	case "off", "выкл":
		a.setMascot(false)
		a.ui.Ok("маскот выключен — Искра ушла спать (вернуть: /mascot on)")
	case "demo", "демо":
		a.mascotDemo()
	case "wave", "hand", "привет":
		a.ui.MascotDance(ui.MascotWink, 1)
		a.ui.MascotSayLine(ui.MascotPurr, "мурр... привет!")
	case "say", "скажи":
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), sub))
		if text == "" {
			a.ui.Warn("формат: /mascot say <текст>")
			return
		}
		a.ui.MascotSpeak(ui.MascotIdle, text)
	default:
		a.ui.Warn("формат: /mascot on|off|demo|say <текст>|wave (сейчас: " + yesNo(a.ui.Mascot()) + ")")
	}
}

// mascotShow — текущий кадр маскота и краткая справка.
func (a *app) mascotShow(text string) {
	a.ui.Println("")
	if text != "" {
		a.ui.MascotSpeak(ui.MascotIdle, text)
	} else {
		a.ui.MascotDance(ui.MascotIdle, 1)
	}
	a.ui.KVPairs([][2]string{
		{"Имя", "Искра — кот gcli, рыжая помощница из темы «Уголь»"},
		{"Состояния", "19: ждёт, думает, работает, радуется, хандрит, спит и другие"},
		{"Сейчас", yesNo(a.ui.Mascot()) + "  ·  вкл/выкл: /mascot on|off"},
		{"Анимации", "demo — все состояния · say <текст> — реплика"},
	})
	a.ui.Println("")
}

// mascotDemo — проиграть все состояния Искры подряд.
func (a *app) mascotDemo() {
	a.ui.Println("")
	a.ui.Section("Искра — все состояния")
	for _, d := range mascotStates {
		a.ui.Println("  " + a.ui.Gray(d.note))
		a.ui.MascotDance(d.s, 1)
		time.Sleep(120 * time.Millisecond)
	}
	a.ui.Println("")
}

// setMascot — включить/выключить маскота и сохранить настройку.
func (a *app) setMascot(on bool) {
	a.repo.Cfg.Mascot = &on
	a.saveConfig()
	a.applyMascot()
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
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "on", "вкл":
		a.repo.Cfg.PlanMode = true
	case "off", "выкл":
		a.repo.Cfg.PlanMode = false
	default:
		a.repo.Cfg.PlanMode = !a.repo.Cfg.PlanMode
	}
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

// minInt — минимум двух чисел (без зависимости от версии Go).
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
