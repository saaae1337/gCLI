package agent

import (
	"fmt"
	"strings"
	"sync"

	"gcli/core"
)

// ---------- Продление хода ----------
//
// Лимит итераций — не стена, а ресурс под запрос. Раньше исчерпание лимита
// означало одно: инструменты отключаются, модель получает приказ писать
// ИТОГОВЫЙ ОТЧЁТ, и вся работа превращается в текст. Для задачи, которая
// требует больше, чем умещается в один ход, это прямое уничтожение
// результата — причём в худший момент, когда агент ещё ничего не сдал.
//
// Поэтому лимит можно продлить. Продление стоит модели ровно одного
// вызова инструмента и требует объяснения: сказать «ещё» — дешево, сказать
// «зачем» — уже заставляет посмотреть на свою работу.
//
// Отказ всегда с причиной. «Нет» без объяснения модель читает как поломку
// и пробует обойти другим способом.

// Константы продления.
const (
	// DefaultExtendStep — на сколько итераций продлевается ход по умолчанию.
	DefaultExtendStep = 30
	// DefaultExtendMax — сколько раз можно продлить за один ход.
	DefaultExtendMax = 8
	// DefaultExtendAbs — абсолютный потолок итераций с продлениями.
	// Ровно то же значение, что и старый потолок в конфиге: продление не
	// увеличивает верхнюю границу, оно лишь распределяет её по требованию.
	DefaultExtendAbs = 200
	// extendReasonMin — минимальная длина обоснования.
	extendReasonMin = 20
	// extendBudgetPct — на скольких процентах израсходованного бюджета
	// продление запрещено. Модель, которая уже съела бюджет, не должна
	// решать за пользователя, тратить ли ещё.
	extendBudgetPct = 90
	// extendProgressMin — минимальная доля новых вызовов за последние
	// итерации, при которой продление считается осмысленным.
	extendProgressMin = 30
	// extendRemindAt — сколько итераций осталось, чтобы начать предупреждать.
	extendRemindAt = 10
)

// ExtendOptions — входные данные для решения о продлении.
type ExtendOptions struct {
	// Base — базовый лимит хода (max_iters).
	Base int
	// Abs — абсолютный потолок с продлениями (max_iters_abs). 0 = дефолт.
	Abs int
	// MaxExtends — сколько раз за ход уже продлили. 0 = лимит не задан.
	MaxExtends int
	// Step — размер одного продления. 0 = дефолт.
	Step int
	// Grants — сколько продлений уже выдано за этот ход.
	Grants int
	// Granted — сколько итераций уже добавлено.
	Granted int
	// Progress — доля новых вызовов за последние итерации (0..100).
	Progress float64
	// BudgetSpent / BudgetLimit — расход сессии и потолок (0 = потолка нет).
	BudgetSpent int
	BudgetLimit int
	// Finalizing — идут финальные ходы: там реестр уже пустой.
	Finalizing bool
	// Reason — обоснование от модели.
	Reason string
	// Want — сколько итераций просит модель (0 = шаг по умолчанию).
	Want int
}

// ExtendVerdict — решение по продлению с объяснением.
type ExtendVerdict struct {
	// Granted — на сколько итераций продлили (0 = отказ).
	Granted int
	// Total — новый лимит хода.
	Total int
	// Reason — обоснование модели (для журнала).
	Reason string
	// Message — текст для модели: что вышло или почему нет.
	Message string
	// OK — выдано ли продление.
	OK bool
}

// ExtendDecision — решить, продлевать ли ход.
//
// Правила проверяются в порядке «от самого грубого к самому тонкому»:
// сначала потолки, потом смысл. Обратный порядок давал бы отказ по
// обоснованию там, где и так упёрлись в абсолютный потолок, и модель
// не понимала бы, что именно мешает.
func ExtendDecision(base int, opt ExtendOptions) ExtendVerdict {
	if base <= 0 {
		base = DefaultMaxIters
	}
	step := opt.Step
	if step <= 0 {
		step = DefaultExtendStep
	}
	abs := opt.Abs
	if abs <= 0 {
		abs = DefaultExtendAbs
	}
	// Потолок никогда не ниже базы: иначе конфиг с max_iters=300 и
	// max_iters_abs=100 дал бы лимит меньше того, который уже работает.
	if abs < base {
		abs = base
	}
	total := base + opt.Granted

	deny := func(msg string) ExtendVerdict {
		return ExtendVerdict{Total: total, Reason: opt.Reason, Message: msg}
	}

	if opt.Finalizing {
		return deny("Продление не работает на финальных ходах: там инструменты уже отключены, " +
			"продлевать нечего. Заканчивай отчёт.")
	}

	reason := strings.TrimSpace(opt.Reason)
	if len([]rune(reason)) < extendReasonMin {
		return deny(fmt.Sprintf("Нужно обоснование: опиши одним-двумя предложениями, что именно осталось сделать "+
			"и почему это не помещается в %d итераций (минимум %d символов). "+
			"«Продли» без причины не пройдёт.", total, extendReasonMin))
	}

	// Сколько просят. Отсутствие n — это «возьми шаг», а не «возьми одну
	// итерацию»: Clamp поднимает 0 до нижней границы, и без этой ветки
	// продление без аргумента тихо давало бы +1 вместо +шаг.
	want := opt.Want
	if want <= 0 {
		want = step
	}
	want = core.Clamp(want, 1, step)

	if opt.Grants >= maxExtends(opt.MaxExtends) {
		return deny(fmt.Sprintf("Лимит продлений исчерпан: %d из %d за этот ход. "+
			"Дальше работа только в следующем ходе — сохрани состояние через handoff и скажи человеку, что осталось.",
			opt.Grants, maxExtends(opt.MaxExtends)))
	}

	if want > 0 && total+want > abs {
		return deny(fmt.Sprintf("Достигнут абсолютный потолок: %d из %d итераций. "+
			"Больше нельзя ни при каких обоснованиях.", total, abs))
	}

	if pct := core.Pct(opt.BudgetSpent, opt.BudgetLimit); opt.BudgetLimit > 0 && pct >= extendBudgetPct {
		return deny(fmt.Sprintf("Бюджет сессии израсходован на %d%% (порог продления — %d%%): "+
			"дальше тратить токены без разрешения человека нельзя. "+
			"Либо закончи, либо попроси у человека /agents budget.", pct, extendBudgetPct))
	}

	if opt.Progress > 0 && opt.Progress < extendProgressMin {
		return deny(fmt.Sprintf("Последние ходы не дают прогресса: только %d%% вызовов были новыми (нужно %d%%). "+
			"Продление в петлю не поможет — смени подход, задай вопрос через ask_user "+
			"или зафиксируй факт через task_note и сдай результат.", int(opt.Progress), extendProgressMin))
	}

	granted := core.Clamp(want, 1, abs-total)
	if granted < 1 {
		return deny(fmt.Sprintf("Абсолютный потолок исчерпан: %d из %d итераций.", total, abs))
	}

	return ExtendVerdict{
		Granted: granted,
		Total:   total + granted,
		Reason:  reason,
		OK:      true,
		Message: fmt.Sprintf("Продлено на %d итераций (новый лимит: %d). Причина: %s",
			granted, total+granted, reason),
	}
}

// maxExtends — сколько продлений разрешено за ход.
func maxExtends(n int) int {
	if n <= 0 {
		return DefaultExtendMax
	}
	return n
}

// ExtendState — состояние продлений текущего хода.
//
// Живёт по одному на ход и переживает само продление: цикл читает лимит из
// него на каждой итерации, а не из локальной переменной.
type ExtendState struct {
	mu       sync.Mutex
	base     int
	abs      int
	maxGrant int
	step     int
	grants   int
	granted  int
	used     bool
	reminded bool
	log      []string
}

// NewExtendState — создать состояние продлений на ход.
func NewExtendState(base, abs, maxExtendsCfg, step int) *ExtendState {
	if base <= 0 {
		base = DefaultMaxIters
	}
	e := &ExtendState{base: base, abs: abs, maxGrant: maxExtendsCfg, step: step}
	if e.abs <= 0 {
		e.abs = DefaultExtendAbs
	}
	if e.abs < e.base {
		e.abs = e.base
	}
	return e
}

// Limit — текущий лимит хода (база + выданные продления).
func (e *ExtendState) Limit() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.base + e.granted
}

// Base — исходный лимит без продлений.
func (e *ExtendState) Base() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.base
}

// Abs — абсолютный потолок итераций хода с учётом продлений.
//
// Нужен хозяину состояния, который поднимает потолок под задание
// (автономный прогон с max_iters=1500): без чтения этого значения
// пришлось бы держать копию лимита рядом и надеяться, что она не
// разъедется с выданными продлениями.
func (e *ExtendState) Abs() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.abs
}

// Extends — сколько раз продлили за этот ход.
func (e *ExtendState) Extends() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.grants
}

// Log — журнал продлений с обоснованиями (для UI и /status).
func (e *ExtendState) Log() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.log...)
}

// Used — выдавалось ли хотя бы одно продление.
func (e *ExtendState) Used() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.used
}

// Reminder — предупреждение о близком лимите (пусто, если не пора).
//
// Порог не «осталось мало», а «осталось меньше, чем нужно на осмысленную
// работу»: 10 итераций хватает, чтобы дописать и проверить, а 2 — только
// чтобы бросить всё и писать отчёт. Отсюда и разница с порогом продления.
func (e *ExtendState) Reminder(left int) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.reminded || e.used {
		return ""
	}
	if left > extendRemindAt || left <= 0 {
		return ""
	}
	e.reminded = true
	return fmt.Sprintf("[Система] До лимита итераций осталось %d — это примерно %d %s работы.\n"+
		"Что делать дальше — решай сам:\n"+
		"1) Если задача почти закончена — заканчивай, проверь результат и сдай отчёт.\n"+
		"2) Если работы много и она идёт с результатом — продли ход инструментом "+
		"extend_turns: объясни одной фразой, что осталось и зачем не помещается.\n"+
		"3) Если упёрся в тупик (не хватает доступа, данных или решения) — спроси "+
		"человека через ask_user или зафиксируй факт через task_note и сдай результат.\n"+
		"Без extend_turns следующие итерации закончатся принудительным отчётом: "+
		"инструменты отключатся, и весь собранный контекст уйдёт в текст.",
		left, left, plural(left, "итерация", "итерации", "итераций"))
}

// Request — модель просит продления. Возвращает решение с объяснением.
//
// Потокобезопасно: инструмент зовётся из горутины инструментов агента.
func (e *ExtendState) Request(progress float64, spent, limit int, reason string, want int) ExtendVerdict {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Повторный запрос в том же ходу, когда уже выдавали, — не новое
	// продление, а проверка состояния: иначе модель могла бы вызвать
	// инструмент десять раз подряд и съесть весь лимит продлений одной
	// идеей.
	if e.used && want <= 0 {
		e.log = append(e.log, fmt.Sprintf("повторная проверка: продлений %d, лимит %d",
			e.grants, e.base+e.granted))
		return ExtendVerdict{
			Total: e.base + e.granted,
			Message: fmt.Sprintf("Продление уже выдано в этом ходу: %d раз(а), лимит %d итераций. Повторно продлевать нельзя.",
				e.grants, e.base+e.granted),
		}
	}

	v := ExtendDecision(e.base, ExtendOptions{
		Abs:         e.abs,
		MaxExtends:  e.maxGrant,
		Step:        e.step,
		Grants:      e.grants,
		Granted:     e.granted,
		Progress:    progress,
		BudgetSpent: spent,
		BudgetLimit: limit,
		Reason:      reason,
		Want:        want,
	})
	if v.OK {
		e.grants++
		e.granted += v.Granted
		e.used = true
		v.Total = e.base + e.granted
	}
	e.log = append(e.log, v.Message)
	return v
}
