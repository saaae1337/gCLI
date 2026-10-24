package main

import (
	"os"
	"strings"

	"gcli/core"
	"gcli/tui"
	"gcli/ui"
)

// ---------- Полноэкранный интерфейс ----------
//
// tui.App владеет экраном и вводом; приложение отдаёт ему события через
// ui.Sink. Здесь — единственное место, где встречаются оба мира: адаптер
// типов, запуск цикла и мостик к подтверждениям.

// tuiSink — адаптер ui.Sink → tui.App.
type tuiSink struct{ app *tui.App }

func (s tuiSink) Text(t string)            { s.app.Text(t) }
func (s tuiSink) BeginStream()             { s.app.BeginStream() }
func (s tuiSink) EndStream()               { s.app.EndStream() }
func (s tuiSink) SetMeta(t string)         { s.app.SetMeta(t) }
func (s tuiSink) BeginThink()              { s.app.BeginThink() }
func (s tuiSink) ThinkText(t string)       { s.app.ThinkText(t) }
func (s tuiSink) EndThink()                { s.app.EndThink() }
func (s tuiSink) ToolStart(tc ui.ToolCall) { s.app.ToolStart(toolInfo(tc)) }
func (s tuiSink) ToolEnd(tc ui.ToolCall)   { s.app.ToolEnd(toolInfo(tc)) }
func (s tuiSink) SetMetrics(m ui.Metrics)  { s.app.SetMetrics(tuiMetrics(m)) }
func (s tuiSink) Clear()                   { s.app.Clear() }

// toolInfo — карточка инструмента в терминах TUI.
func toolInfo(tc ui.ToolCall) tui.ToolInfo {
	return tui.ToolInfo{
		ID:      tc.ID,
		Name:    tc.Name,
		Args:    tc.Args,
		Kind:    tc.Kind,
		Detail:  tc.Detail,
		Status:  tc.Status,
		Elapsed: tc.Elapsed,
		Lines:   tc.Lines,
	}
}

// tuiMetrics — снимок расхода в терминах TUI.
func tuiMetrics(m ui.Metrics) tui.Metrics {
	return tui.Metrics{
		Model:      m.Model,
		Provider:   m.Provider,
		Context:    m.Context,
		ContextMax: m.ContextMax,
		Prompt:     m.Prompt,
		Completion: m.Completion,
		Requests:   m.Requests,
		Rate:       m.Rate,
		Last:       m.Last,
		Session:    m.Session,
		Cost:       m.Cost,
		CostKnown:  m.CostKnown,
		Autopilot:  m.Autopilot,
		Plan:       m.Plan,
	}
}

// tuiCommands — палитра по «/»: только то, что действительно есть в
// обработчике команд. Подсказки короткие — это не справка, а напоминание.
var tuiCommands = []tui.Command{
	cmd("/help", "справка по командам"),
	cmd("/model", "сменить модель"),
	cmd("/models", "список моделей провайдера"),
	cmd("/provider", "провайдер и ключи"),
	cmd("/setup", "мастер настройки"),
	cmd("/status", "состояние сессии и ключей"),
	cmd("/usage", "расход токенов и деньги"),
	cmd("/stats", "дашборд по сессиям"),
	cmd("/think", "показ размышлений"),
	cmd("/plan", "режим плана"),
	cmd("/agent", "агентный режим"),
	cmd("/agents", "субагенты"),
	cmd("/autopilot", "автопилот"),
	cmd("/yolo", "без подтверждений"),
	cmd("/permissions", "правила разрешений"),
	cmd("/sandbox", "песочница файлов"),
	cmd("/snapshots", "снимки проекта"),
	cmd("/mission", "автономный прогон"),
	cmd("/cron", "автономные итерации: поиск и исправление багов"),
	cmd("/todos", "план работ"),
	cmd("/context", "что в контексте"),
	cmd("/compact", "сжать историю"),
	cmd("/sessions", "сессии"),
	cmd("/fork", "ветка сессии"),
	cmd("/resume", "возобновить сессию"),
	cmd("/undo", "отменить правку"),
	cmd("/revert", "откатить ход"),
	cmd("/export", "экспорт диалога"),
	cmd("/memory", "заметки проекта"),
	cmd("/rules", "правила проекта"),
	cmd("/skills", "навыки"),
	cmd("/ext", "расширения"),
	cmd("/tools", "инструменты"),
	cmd("/mcp", "MCP-серверы"),
	cmd("/doctor", "диагностика"),
	cmd("/init", "создать GCLI.md"),
	cmd("/clear", "новая сессия"),
	cmd("/cls", "очистить экран"),
	cmd("/quit", "выход"),
}

// cmd — элемент палитры.
func cmd(name, desc string) tui.Command {
	return tui.Command{Name: name, Desc: desc}
}

// tuiColors — глубина цвета для рендерера TUI: truecolor, 256 или 16.
//
// Цвет определяет пакет ui (флаги -color/GCLI_COLOR и возможности
// терминала). TUI обязан её уважать: Terminal.app на macOS не понимает
// 24-битные SGR и без пересчёта потерял бы всю палитру.
func tuiColors(l ui.ColorLevel) int {
	switch l {
	case ui.Color16:
		return 16
	case ui.Color256:
		return 256
	}
	return 0
}

// tuiBlocked — режим запуска, в котором полноэкранный интерфейс не нужен.
var tuiBlocked bool

// tuiReady — стоит ли включать полноэкранный интерфейс.
func (a *app) tuiReady(tty, useUnicode, machine bool) bool {
	if machine || !tty || !useUnicode || tuiBlocked {
		return false
	}
	if os.Getenv("GCLI_TUI") == "0" {
		return false
	}
	return stdinIsTerminal()
}

// startTUI — создать интерфейс и подписать на него старый ui.
//
// Возвращает false, если терминал не подходит; тогда вызывающий создаёт
// обычный stdinReader и работает по-старому.
func (a *app) startTUI() bool {
	if !a.tuiWanted {
		return false
	}
	app := tui.New(tui.Options{
		Version:  version(),
		Model:    a.model,
		Provider: a.prov.Label,
		WorkDir:  a.workDir,
		Session:  a.sess.ID,
		Commands: tuiCommands,
		Color:    a.ui.ColorLevel() != 0,
		Colors:   tuiColors(a.ui.ColorLevel()),
		Unicode:  true,
		// GCLI_MASCOT=0 и настройка mascot в конфиге действуют и здесь:
		// заставка — единственное место, где кот виден в TUI.
		HideMascot: !a.ui.Mascot(),
		// Автономные итерации — бейдж в строке статуса TUI.
		CronEnabled: a.cronEnabled,
		In:          os.Stdin,
		Out:         os.Stdout,
		Raw:         makeRawTUI,
		Size:        termSize,
	})
	a.setTUI(app)
	// Sink подключается перед запуском цикла (replTUI): до этого сообщения
	// старта должны быть видны в обычном терминале, даже если запуск
	// оборвётся до первого кадра.
	if w, _ := termSize(); w > 20 {
		a.ui.SetWidth(w)
	}
	return true
}

// tuiApp — текущий полноэкранный интерфейс или nil.
//
// Указатель меняется при запуске и откате, а читают его горутина сигналов
// и инструменты: без общей блокировки это была бы гонка на ровном месте.
func (a *app) tuiApp() *tui.App {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tui
}

// setTUI — запомнить или снять полноэкранный интерфейс.
func (a *app) setTUI(t *tui.App) {
	a.mu.Lock()
	a.tui = t
	a.mu.Unlock()
}

// replTUI — интерактивный цикл в полноэкранном режиме.
func (a *app) replTUI() {
	app := a.tuiApp()
	if app == nil {
		return
	}
	// Вывод старых панелей идёт в ленту только на время цикла. До него
	// сообщения старта печатались в терминал и остаются в прокрутке, даже
	// если запуск оборвётся.
	a.ui.SetSink(tuiSink{app: app})
	defer a.ui.SetSink(nil)

	// Замечания старта — в ленту, а не в баннер: баннера здесь нет.
	for _, n := range append(append([]string{}, a.setupNotes...), a.startupNotes...) {
		app.Text("  " + n + "\n")
	}
	a.ui.SetMetrics(a.metrics())

	err := app.Run(tui.Handlers{
		Submit:    a.tuiSubmit,
		Interrupt: a.tuiInterrupt,
	})
	if err == nil {
		// Альтернативный экран уже вернул прежнее содержимое терминала:
		// прощаемся обычной строкой, как старый REPL по Ctrl+D.
		a.ui.SetSink(nil)
		a.setTUI(nil)
		if !a.quiet {
			a.ui.Info("до связи!")
		}
		return
	}
	// Raw-режим мог отказать (терминал без управления): честно
	// откатываемся в обычный REPL и говорим об этом — с подсказкой,
	// что именно не так и как обойти.
	a.ui.SetSink(nil)
	a.setTUI(nil)
	if a.stdin == nil {
		a.stdin = newStdin()
	}
	a.banner()
	a.ui.Warn("полноэкранный интерфейс недоступен: " + err.Error() + rawTUIHint())
	a.stdin.enableRaw()
	defer a.stdin.disableRaw()
	keys := a.startWatcher(a.ctrlO)
	defer keys.stopWatcher()
	a.repl()
}

// tuiSubmit — ход из полноэкранного интерфейса.
func (a *app) tuiSubmit(text string) {
	line := strings.TrimSpace(text)
	if line == "" {
		return
	}
	if strings.HasPrefix(line, "/") {
		if !a.handleCommand(line) {
			if t := a.tuiApp(); t != nil {
				t.Quit()
			}
		}
		a.ui.SetMetrics(a.metrics())
		return
	}
	if strings.HasPrefix(line, "#") {
		a.appendMemoryNote(line)
		return
	}
	if err := a.turn(line); err != nil {
		a.ui.Err(err.Error())
	}
	a.ui.SetMetrics(a.metrics())
	// Занятость снимет defer в tui.runSubmit: если здесь вызвать Busy(false),
	// очередь стартовала бы дважды.
}

// tuiInterrupt — Esc/Ctrl+C во время хода.
func (a *app) tuiInterrupt() {
	a.mu.Lock()
	c := a.cancel
	a.mu.Unlock()
	if c != nil {
		c()
	}
}

// askLine — прочитать строку: в TUI — окном, иначе — из stdin.
func (a *app) askLine(question string) (string, bool) {
	if t := a.tuiApp(); t != nil && t.Running() {
		return t.Ask(tui.Ask{Question: question})
	}
	if a.stdin == nil || !a.stdin.Scan() {
		return "", false
	}
	return strings.TrimSpace(a.stdin.Text()), true
}

// askSecret — то же, но ввод скрыт (ключи).
func (a *app) askSecret(question string) (string, bool) {
	if t := a.tuiApp(); t != nil && t.Running() {
		return t.Ask(tui.Ask{Question: question, Secret: true})
	}
	if a.stdin == nil || !a.stdin.Scan() {
		return "", false
	}
	return strings.TrimSpace(a.stdin.Text()), true
}

// tuiConfirm — подтверждение окном (nil = интерфейс не запущен).
func (a *app) tuiConfirm(c tui.Confirm) (tui.Choice, bool) {
	t := a.tuiApp()
	if t == nil || !t.Running() {
		return tui.ChoiceNo, false
	}
	return t.Confirm(c), true
}

// relPath — путь для окна подтверждения: короче и без рабочего каталога.
func (a *app) relPath(p string) string {
	return core.RelToWD(a.workDir, p)
}

// tuiAskUser — вопрос инструмента ask_user в полноэкранном режиме.
func (a *app) tuiAskUser(question string, options []string) (string, bool) {
	t := a.tuiApp()
	if t == nil || !t.Running() {
		return "", false
	}
	return t.Ask(tui.Ask{Question: question, Options: options})
}
