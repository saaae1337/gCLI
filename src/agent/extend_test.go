package agent

import (
	"strings"
	"testing"
)

// ---------- Тесты продления хода ----------
//
// Продление — механизм, где важен не «выдалось/не выдалось», а ПРИЧИНА
// отказа. Модель читает текст решения и по нему выбирает следующий шаг:
// запродлить, сменить подход или сдать результат. Поэтому почти каждый тест
// проверяет не только вердикт, но и наличие в сообщении конкретной подсказки.

const goodReason = "осталось дописать тесты и прогнать их, это не помещается в текущий лимит"

// TestExtendDecisionGrants — базовый случай: обоснование есть, потолки не мешают.
func TestExtendDecisionGrants(t *testing.T) {
	v := ExtendDecision(40, ExtendOptions{Reason: goodReason, Progress: 100})
	if !v.OK {
		t.Fatalf("продление должно выдаться: %s", v.Message)
	}
	if v.Granted != DefaultExtendStep {
		t.Errorf("выдано %d, ждали шаг по умолчанию %d", v.Granted, DefaultExtendStep)
	}
	if v.Total != 40+DefaultExtendStep {
		t.Errorf("новый лимит %d, ждали %d", v.Total, 40+DefaultExtendStep)
	}
}

// TestExtendDecisionRequiresReason — обоснование обязательно.
//
// Это главная защита механизма: без неё «продли» превращается в бесплатную
// кнопку, и лимит перестаёт быть ресурсом.
func TestExtendDecisionRequiresReason(t *testing.T) {
	for _, reason := range []string{"", "   ", "ещё"} {
		v := ExtendDecision(40, ExtendOptions{Reason: reason, Progress: 100})
		if v.OK {
			t.Errorf("обоснование %q: продление не должно выдаться", reason)
		}
		if v.Granted != 0 {
			t.Errorf("обоснование %q: выдано %d итераций вместо отказа", reason, v.Granted)
		}
		if !strings.Contains(v.Message, "обоснование") {
			t.Errorf("обоснование %q: в отказе нет требования объяснить: %s", reason, v.Message)
		}
	}
}

// TestExtendDecisionReasonLength — короткое по числу символов обоснование
// отклоняется даже когда текст осмысленный («нужно ещё»).
func TestExtendDecisionReasonLength(t *testing.T) {
	short := strings.Repeat("а", extendReasonMin-1)
	v := ExtendDecision(40, ExtendOptions{Reason: short, Progress: 100})
	if v.OK {
		t.Error("обоснование короче минимума не должно проходить")
	}
	ok := strings.Repeat("а", extendReasonMin)
	if v := ExtendDecision(40, ExtendOptions{Reason: ok, Progress: 100}); !v.OK {
		t.Errorf("обоснование ровно в %d символов должно проходить: %s", extendReasonMin, v.Message)
	}
}

// TestExtendDecisionMaxExtends — потолок числа продлений за ход.
func TestExtendDecisionMaxExtends(t *testing.T) {
	v := ExtendDecision(40, ExtendOptions{Reason: goodReason, Grants: 3, MaxExtends: 3, Progress: 100})
	if v.OK {
		t.Fatal("третье из трёх продлений выдаваться не должно")
	}
	if !strings.Contains(v.Message, "исчерпан") {
		t.Errorf("в отказе нет упоминания исчерпания: %s", v.Message)
	}
}

// TestExtendDecisionAbsCeiling — абсолютный потолок не обходится.
func TestExtendDecisionAbsCeiling(t *testing.T) {
	v := ExtendDecision(40, ExtendOptions{Reason: goodReason, Granted: 170, Abs: 200, Progress: 100})
	if v.OK {
		t.Fatalf("при остатке в %d итерации и шаге %d продление не должно выдаться", 200-210, DefaultExtendStep)
	}
	if !strings.Contains(v.Message, "потолок") {
		t.Errorf("в отказе нет упоминания потолка: %s", v.Message)
	}
}

// TestExtendDecisionAbsNeverBelowBase — конфиг с абсолютным потолком меньше
// базы не должен урезать работающий лимит: потолок поднимается до базы.
//
// Продление при этом не выдаётся, и это правильно: при базе 300 и потолке
// 100 места для роста нет, а выдать +1 «в обход потолка» нельзя.
func TestExtendDecisionAbsNeverBelowBase(t *testing.T) {
	v := ExtendDecision(300, ExtendOptions{Reason: goodReason, Abs: 100, Step: 10, Progress: 100})
	if v.OK {
		t.Errorf("при базе 300 и потолке 100 продлений выдаваться не может, получено %+v", v)
	}
	if v.Total != 300 {
		t.Errorf("total %d, ждали базу 300 — потолок не должен ужимать лимит", v.Total)
	}
	// А вот потолок выше базы — продление обычное.
	if v := ExtendDecision(300, ExtendOptions{Reason: goodReason, Abs: 400, Step: 10, Progress: 100}); !v.OK {
		t.Errorf("при потолке 400 продление должно выдаться: %s", v.Message)
	}
}

// TestExtendDecisionBudgetGate — израсходованный бюджет блокирует продление.
func TestExtendDecisionBudgetGate(t *testing.T) {
	v := ExtendDecision(40, ExtendOptions{
		Reason: goodReason, Progress: 100, BudgetSpent: 95, BudgetLimit: 100,
	})
	if v.OK {
		t.Fatal("при 95% израсходованного бюджета продление не должно выдаваться")
	}
	if !strings.Contains(v.Message, "Бюджет") {
		t.Errorf("в отказе нет упоминания бюджета: %s", v.Message)
	}
	// Чуть ниже порога — проходит.
	if v := ExtendDecision(40, ExtendOptions{
		Reason: goodReason, Progress: 100, BudgetSpent: 89, BudgetLimit: 100,
	}); !v.OK {
		t.Errorf("при 89 процентах бюджета продление должно выдаться: %s", v.Message)
	}
}

// TestExtendDecisionNoBudgetNoGate — без потолка бюджета продление не блокируется.
func TestExtendDecisionNoBudgetNoGate(t *testing.T) {
	v := ExtendDecision(40, ExtendOptions{Reason: goodReason, Progress: 100, BudgetSpent: 10_000_000})
	if !v.OK {
		t.Errorf("без BudgetLimit бюджет не должен мешать: %s", v.Message)
	}
}

// TestExtendDecisionProgressGate — работа без новых вызовов продления не
// заслуживает: дополнительные итерации уйдут в ту же петлю.
func TestExtendDecisionProgressGate(t *testing.T) {
	v := ExtendDecision(40, ExtendOptions{Reason: goodReason, Progress: 10})
	if v.OK {
		t.Fatal("при 10% новых вызовов продление не должно выдаваться")
	}
	if !strings.Contains(v.Message, "ask_user") {
		t.Errorf("в отказе нет подсказки, что делать вместо продления: %s", v.Message)
	}
	// Progress == 0 — данных мало, проверка не применяется.
	if v := ExtendDecision(40, ExtendOptions{Reason: goodReason, Progress: 0}); !v.OK {
		t.Errorf("при Progress=0 проверка не должна срабатывать: %s", v.Message)
	}
}

// TestExtendDecisionFinalizing — на финальных ходах продлевать нечего.
func TestExtendDecisionFinalizing(t *testing.T) {
	v := ExtendDecision(40, ExtendOptions{Reason: goodReason, Progress: 100, Finalizing: true})
	if v.OK {
		t.Fatal("на финальных ходах продление не должно выдаваться")
	}
	if !strings.Contains(v.Message, "финальных") {
		t.Errorf("в отказе нет упоминания финальных ходов: %s", v.Message)
	}
}

// TestExtendDecisionStepCap — модель не может попросить больше шага.
func TestExtendDecisionStepCap(t *testing.T) {
	v := ExtendDecision(40, ExtendOptions{Reason: goodReason, Step: 30, Want: 500, Progress: 100})
	if !v.OK {
		t.Fatalf("просьба продления должна выдаться: %s", v.Message)
	}
	if v.Granted != 30 {
		t.Errorf("выдано %d, ждали потолок шага 30", v.Granted)
	}
}

// TestExtendDecisionWantSmaller — модель вправе попросить меньше шага.
func TestExtendDecisionWantSmaller(t *testing.T) {
	v := ExtendDecision(40, ExtendOptions{Reason: goodReason, Step: 30, Want: 7, Progress: 100})
	if !v.OK || v.Granted != 7 {
		t.Errorf("хотели 7 итераций, получили вердикт %+v", v)
	}
}

// TestExtendDecisionZeroWant — отсутствие n равно шагу по умолчанию.
func TestExtendDecisionZeroWant(t *testing.T) {
	v := ExtendDecision(40, ExtendOptions{Reason: goodReason, Progress: 100, Want: 0})
	if !v.OK || v.Granted != DefaultExtendStep {
		t.Errorf("без n ждали шаг %d, получили %+v", DefaultExtendStep, v)
	}
}

// TestExtendStateLimitGrows — лимит растёт ровно на выданное.
func TestExtendStateLimitGrows(t *testing.T) {
	e := NewExtendState(40, 200, 3, 10)
	if e.Limit() != 40 {
		t.Fatalf("до продления лимит %d, ждали 40", e.Limit())
	}
	v := e.Request(100, 0, 0, goodReason, 0)
	if !v.OK {
		t.Fatalf("продление не выдалось: %s", v.Message)
	}
	if e.Limit() != 50 {
		t.Errorf("после продления лимит %d, ждали 50", e.Limit())
	}
	if e.Extends() != 1 {
		t.Errorf("продлений %d, ждали 1", e.Extends())
	}
	if !e.Used() {
		t.Error("Used должен быть true после выдачи продления")
	}
	if len(e.Log()) == 0 {
		t.Error("журнал решений должен что-то содержать")
	}
}

// TestExtendStateRepeatRequestWithoutWant — повторный вызов без n не должен
// съедать лимит продлений: это проверка состояния, а не новое продление.
func TestExtendStateRepeatRequestWithoutWant(t *testing.T) {
	e := NewExtendState(40, 200, 3, 10)
	if v := e.Request(100, 0, 0, goodReason, 0); !v.OK {
		t.Fatalf("первое продление не выдалось: %s", v.Message)
	}
	before := e.Limit()
	v := e.Request(100, 0, 0, goodReason, 0)
	if v.OK || v.Granted != 0 {
		t.Errorf("повторный запрос без n должен отказать, а не продлить: %+v", v)
	}
	if e.Limit() != before {
		t.Errorf("лимит изменился при отказе: %d → %d", before, e.Limit())
	}
	if e.Extends() != 1 {
		t.Errorf("продлений стало %d, ждали 1", e.Extends())
	}
}

// TestExtendStateDenyDoesNotGrow — отказ по любой причине не двигает лимит.
func TestExtendStateDenyDoesNotGrow(t *testing.T) {
	for name, opt := range map[string]ExtendOptions{
		"без причины":  {Reason: "ещё", Progress: 100},
		"по бюджету":   {Reason: goodReason, Progress: 100, BudgetSpent: 100, BudgetLimit: 100},
		"по прогрессу": {Reason: goodReason, Progress: 5},
		"потолок":      {Reason: goodReason, Progress: 100, Granted: 199, Abs: 200, Step: 30},
		"лимит выдан":  {Reason: goodReason, Grants: 3, MaxExtends: 3, Progress: 100},
	} {
		e := NewExtendState(40, 200, 3, 10)
		// Готовим состояние для варианта с лимитом выданных продлений.
		if opt.Grants > 0 {
			for i := 0; i < opt.Grants; i++ {
				e.Request(100, 0, 0, goodReason, 10)
			}
		}
		if opt.Granted > 0 {
			// Имитируем уже выданные итерации без запроса.
			e.granted = opt.Granted
		}
		before := e.Limit()
		v := e.Request(opt.Progress, opt.BudgetSpent, opt.BudgetLimit, opt.Reason, opt.Want)
		if v.OK {
			t.Errorf("%s: отказ ожидался, получено %+v", name, v)
		}
		if e.Limit() != before {
			t.Errorf("%s: отказ изменил лимит %d → %d", name, before, e.Limit())
		}
		if v.Message == "" {
			t.Errorf("%s: отказ без объяснения", name)
		}
	}
}

// TestExtendStateReminderFiresOnceNearLimit — предупреждение приходит
// заранее и ровно один раз: повторять его каждую итерацию — жечь контекст.
func TestExtendStateReminderFiresOnceNearLimit(t *testing.T) {
	e := NewExtendState(40, 200, 3, 10)
	if msg := e.Reminder(30); msg != "" {
		t.Error("при 30 итерациях запаса предупреждать рано")
	}
	msg := e.Reminder(5)
	if msg == "" {
		t.Fatal("при 5 итерациях запаса ожидалось предупреждение")
	}
	for _, want := range []string{"extend_turns", "ask_user"} {
		if !strings.Contains(msg, want) {
			t.Errorf("в предупреждении нет подсказки про %s", want)
		}
	}
	if again := e.Reminder(4); again != "" {
		t.Error("предупреждение повторилось: должно быть одно на ход")
	}
}

// TestExtendStateReminderSkippedAfterExtend — продливший агент уже знает
// про лимит, повторное предупреждение только путает.
func TestExtendStateReminderSkippedAfterExtend(t *testing.T) {
	e := NewExtendState(40, 200, 3, 10)
	if v := e.Request(100, 0, 0, goodReason, 0); !v.OK {
		t.Fatalf("продление не выдалось: %s", v.Message)
	}
	if msg := e.Reminder(3); msg != "" {
		t.Error("после продления предупреждение повторяться не должно")
	}
}

// TestExtendTurnOutsideTurnNoPanic — инструмент доступен и вне хода, когда
// a.ext равен nil. Раньше здесь был обход указателя: паника убивала ход.
func TestExtendTurnOutsideTurnNoPanic(t *testing.T) {
	a := newTestAgent()
	v := a.ExtendTurn(goodReason, 0)
	if v.OK {
		t.Error("вне хода продление выдаваться не должно")
	}
	if !strings.Contains(v.Message, "только во время хода") {
		t.Errorf("в отказе нет объяснения, что хода нет: %s", v.Message)
	}
	if a.ExtendUsed() {
		t.Error("вне хода Used должен быть false")
	}
	if a.ExtendState() != nil {
		t.Error("вне хода ExtendState должен быть nil")
	}
	if len(a.ExtendLog()) != 0 {
		t.Error("вне хода журнал должен быть пустым")
	}
}

// TestExtendTurnOnFinalizingTurnDenied — на финальных ходах инструменты уже
// отключены (AgentMode выключен), и продление обязано отказать.
func TestExtendTurnOnFinalizingTurnDenied(t *testing.T) {
	a := newTestAgent()
	a.ext = NewExtendState(40, 200, 3, 10)
	a.AgentMode = false // состояние финализации
	v := a.ExtendTurn(goodReason, 0)
	if v.OK {
		t.Error("на финальных ходах продление выдаваться не должно")
	}
	if !strings.Contains(v.Message, "финальных") {
		t.Errorf("в отказе нет упоминания финальных ходов: %s", v.Message)
	}
	if a.ext.Limit() != 40 {
		t.Errorf("отказ изменил лимит: %d", a.ext.Limit())
	}
}

// TestExtendStateAbsDefaults — потолок никогда не ниже базы.
func TestExtendStateAbsDefaults(t *testing.T) {
	if got := NewExtendState(300, 100, 0, 0).Limit(); got != 300 {
		t.Errorf("лимит %d, ждали базу 300", got)
	}
	if got := NewExtendState(40, 0, 0, 0).abs; got != DefaultExtendAbs {
		t.Errorf("потолок по умолчанию %d, ждали %d", got, DefaultExtendAbs)
	}
}
