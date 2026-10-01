package ui

import (
	"fmt"
	"strings"

	"gcli/core"
)

// ---------- Прогресс пакетных операций ----------
//
// Инструменты multi_* и spawn_agents выполняют N целей параллельно и шлют
// события из своих горутин. Без живой обратной связи пользователь видит
// «( >.< ) working...» полминуты и не понимает, завис ли агент или просто
// читает двадцать файлов. События решают это: видно и «3/7», и то, какая
// именно цель упала.
//
// Формат — одна строка на событие, вровень с рельсом вызовов:
//
//	  ⎿ multi_read 3/7 ✓ a.go
//	  ⎿ multi_bash 2/3 ✗ go test ./tools/ — ненулевой код выхода: 1
//	  ✓ multi_read: 7 прочитано, 0 с ошибкой
//
// Это журнал, а не перерисовываемая строка: события копятся в выводе и по
// концу сессии показывают, что происходило. Одна строка с перезаписью
// выглядела бы аккуратнее, но требует управления курсором, которое ломается
// при любом постороннем выводе — а он есть почти всегда.

// ProgressUpdate — событие прогресса на языке UI.
//
// Отдельный тип, а не tools.ProgressEvent: пакет ui ничего не знает про
// инструменты, приложение переводит одно в другое на границе.
type ProgressUpdate struct {
	// Title — операция: multi_read, multi_bash, spawn_agents.
	Title string
	// Label — что выполняется: путь, команда, имя субагента.
	Label string
	// Done / Total — выполнено целей из общего числа.
	Done  int
	Total int
	// Ok — цель без ошибки.
	Ok bool
	// Err — текст ошибки цели.
	Err string
	// Final — операция завершена целиком (последнее событие).
	Final bool
}

// Progress — напечатать событие прогресса.
//
// Безопасен для вызова из горутин инструмента: вывод идёт под u.mu, поэтому
// события разных целей не перемешиваются и не вклиниваются в поток ответа
// модели. Порядок печати — порядок завершения целей, а не порядок аргументов;
// для журнала это честнее: видно, что шло быстрее.
func (u *UI) Progress(ev ProgressUpdate) {
	if u.quiet {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	// Печать гасит строку ожидания: иначе прогресс перерисовывается
	// поверх неё и терминал получает кашу из escape-последовательностей.
	u.clearSpinLocked()
	u.mascotAnchored = false

	// Отступ добавляется здесь, а не внутри progressLine: иначе строка
	// получалась шириной на весь отступ больше терминала.
	_, _ = u.out.Write([]byte(u.progressLine(ev) + "\n"))
}

// progressLine — собрать одну строку прогресса. Вызывается под mu.
func (u *UI) progressLine(ev ProgressUpdate) string {
	indent := u.railIndent()
	// Финальное событие — сводка, а не очередная цель. Печатаем его строкой
	// состояния, а не «7/7»: после него ToolEnd всё равно добавит свою
	// строку, и два подряд одинаковых счётчика только сбивают с толку.
	if ev.Final {
		text := core1Line(ev.Label)
		if text == "" {
			text = "готово"
		}
		mark := " " + u.Green(u.g.Check) + " "
		if ev.Err != "" {
			mark = " " + u.Red(u.g.Cross) + " "
			text = text + ": " + ev.Err
		}
		return indent + u.emberTail() + mark + u.truncCols(text, u.Width()-visibleWidth(indent+u.emberTail()+mark))
	}

	head := " " + u.Accent(ev.Title) + u.progressCount(ev) + " "
	mark := u.Green(u.g.Check)
	if !ev.Ok {
		mark = u.Yellow(u.g.Excl)
	}
	// Ширина считается от реально собранного префикса, а не «на глаз»:
	// в него входят отступ рельса, зубец, название операции, счётчик и знак
	// статуса. Наивная константа давала строку шириной 33 при терминале 30 —
	// и обрезка не спасала, потому что неверно начиналась.
	prefix := indent + u.emberTail() + head + mark + " "
	avail := u.Width() - visibleWidth(prefix)
	avail = core.Max(avail, 8)

	// Ошибка цели — самое важное, что здесь случается: модель увидит её в
	// итоговом тексте пачки, а пользователь раньше только финальную сводку.
	// Место делим между подписью цели и текстом ошибки пополам: обрезанная
	// команда («go test ./...») всё ещё опознаётся, обрезанная ошибка —
	// нет.
	if ev.Err != "" {
		sep := " — "
		// Сначала обеспечиваем узнаваемую подпись цели (как минимум
		// 12 колонок — этого хватает, чтобы отличить «go test ./...» от
		// «go build ./...»), остальное отдаём тексту ошибки.
		labelWant := runeLen(ev.Label)
		if labelWant > 12 {
			labelWant = 12
		}
		errWant := runeLen(ev.Err)
		if room := avail - labelWant - runeLen(sep); errWant > room {
			errWant = room
		}
		if errWant < 8 {
			errWant = 8
		}
		label := u.truncCols(ev.Label, avail-errWant-runeLen(sep))
		return prefix + label + u.Yellow(sep+u.truncCols(ev.Err, errWant))
	}
	return prefix + u.truncCols(ev.Label, avail)
}

// progressCount — «3/7» цветом, при неизвестном размере — пусто.
func (u *UI) progressCount(ev ProgressUpdate) string {
	if ev.Total <= 0 {
		return ""
	}
	return u.c(u.pal.Muted, fmt.Sprintf(" %d/%d", ev.Done, ev.Total))
}

// truncCols — обрезать строку до n колонок терминала.
func (u *UI) truncCols(s string, n int) string {
	if n < 6 {
		n = 6
	}
	if visibleWidth(s) <= n {
		return s
	}
	return truncate(s, n)
}

// core1Line — однострочная сводка без переносов.
func core1Line(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}
