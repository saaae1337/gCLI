package main

import (
	"strings"
	"sync"

	"gcli/core"
)

// ---------- Вопросы у терминала ----------
//
// Единственный читатель stdin - композер REPL. Подтверждения инструментов,
// мастер настройки и ask_user идут через его канал askCh: вопрос печатается
// в стенку вывода над полем ввода, ответ набирается в самом поле. Пайпы и
// CI (композера нет) читают строки напрямую, как раньше.

var (
	composerMu sync.Mutex
	composerP  *composer
)

// setComposer - запомнить текущий композер (nil - REPL завершён).
func (a *app) setComposer(c *composer) {
	composerMu.Lock()
	composerP = c
	composerMu.Unlock()
}

// composerRef - текущий композер или nil.
func (a *app) composerRef() *composer {
	composerMu.Lock()
	defer composerMu.Unlock()
	return composerP
}

// askLine - прочитать строку у человека. Композер активен - вопрос идёт
// через него; иначе - прямой Scan (пайп, CI, тесты).
func (a *app) askLine(question string) (string, bool) {
	if c := a.composerRef(); c != nil {
		return c.ask(question, false, nil)
	}
	if a.stdin == nil || !a.stdin.Scan() {
		return "", false
	}
	return strings.TrimSpace(a.stdin.Text()), true
}

// askSecret - то же, но ввод скрыт (ключи).
func (a *app) askSecret(question string) (string, bool) {
	if c := a.composerRef(); c != nil {
		return c.ask(question, true, nil)
	}
	if a.stdin == nil || !a.stdin.Scan() {
		return "", false
	}
	return strings.TrimSpace(a.stdin.Text()), true
}

// askOptions - вопрос с вариантами (номер варианта или свой ответ).
func (a *app) askOptions(question string, options []string) (string, bool) {
	if c := a.composerRef(); c != nil {
		return c.ask(question, false, options)
	}
	if a.quiet {
		return "", false
	}
	a.ui.Println("")
	a.ui.Println("  " + a.ui.Accent(a.ui.Glyphs().Dot) + " " + question)
	for i, opt := range options {
		a.ui.Println("    " + a.ui.Gray(itoaMain(i+1)+".") + " " + opt)
	}
	if a.stdin == nil || !a.stdin.Scan() {
		return "", false
	}
	return strings.TrimSpace(a.stdin.Text()), true
}

func itoaMain(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// canAskTerminal - можно ли текущему ходу задать человеку вопрос у
// терминала. Композер обслуживает вопросы из любого хода (ход из cron
// подождёт ответа в своей горутине); без композера вопрос допустим только
// у хода из читателя ввода: фоновый ход отказывает сам себе (denyBackground)
// - это честнее молчаливого ожидания.
func (a *app) canAskTerminal() bool {
	if a.composerRef() != nil {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.turnInteractive
}

// relPath - путь для окна подтверждения: короче и без рабочего каталога.
func (a *app) relPath(p string) string {
	return core.RelToWD(a.workDir, p)
}
