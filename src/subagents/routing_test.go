package subagents

import (
	"strings"
	"testing"

	"gcli/core"
)

// ---------- Бюджетная маршрутизация моделей ----------

// routeOpts — типовой набор для маршрутизатора: дорогая текущая модель,
// дешёвая доступная, бюджет не задан.
func routeOpts() RouteOptions {
	return RouteOptions{
		Current:    "claude-opus-4",
		ProviderID: "anthropic",
		Available:  []string{"claude-opus-4", "claude-sonnet-4", "claude-3-5-haiku"},
		Enabled:    true,
	}
}

// TestRouteKeepsExplicitModel — модель, названная моделью, не трогается никогда.
// Человек сказал «opus» — значит opus, даже на пределе бюджета.
func TestRouteKeepsExplicitModel(t *testing.T) {
	opts := routeOpts()
	opts.Explicit = "claude-sonnet-4"
	opts.Budget = Budget{Spent: 999999, Limit: 1000}
	v := RouteModel(TypeCoder, opts)
	if v.Model != opts.Current {
		t.Errorf("явная модель заменена на %q", v.Model)
	}
	if v.Downgraded {
		t.Error("явная модель не должна помечаться как пониженная")
	}
}

// TestRouteDisabledByDefault — молчаливая подмена модели дороже её отсутствия:
// пользователь узнаёт о ней по счёту.
func TestRouteDisabledByDefault(t *testing.T) {
	opts := routeOpts()
	opts.Enabled = false
	opts.Budget = Budget{Spent: 100, Limit: 100}
	v := RouteModel(TypeExplorer, opts)
	if v.Model != opts.Current {
		t.Errorf("при выключенной маршрутизации модель изменена на %q", v.Model)
	}
}

// TestRouteCheapRoleGoesCheap — простая роль едет на дешёвую модель даже при
// здоровом бюджете: платить цену opus за карту проекта незачем.
func TestRouteCheapRoleGoesCheap(t *testing.T) {
	v := RouteModel(TypeExplorer, routeOpts())
	if !v.Downgraded {
		t.Fatalf("простая роль не понижена: %s", v.Why)
	}
	// claude-3-5-haiku — самая дешёвая в наборе.
	if v.Model != "claude-3-5-haiku" {
		t.Errorf("выбрана %q, ожидалась claude-3-5-haiku (%s)", v.Model, v.Why)
	}
	if v.Why == "" {
		t.Error("решение без объяснения — его нечем объяснить пользователю")
	}
}

// TestRouteDemandingRoleKeepsExpensiveModel — требовательная роль при здоровом
// бюджете остаётся на текущей модели: менять там нечего.
func TestRouteDemandingRoleKeepsExpensiveModel(t *testing.T) {
	v := RouteModel(TypeCoder, routeOpts())
	if v.Downgraded {
		t.Errorf("требовательная роль понижена без причины: %s", v.Why)
	}
	if v.Model != "claude-opus-4" {
		t.Errorf("модель изменена на %q", v.Model)
	}
}

// TestRouteTightBudgetDowngradesDemanding — на пределе бюджета понижается всё,
// включая роли, которым качество модели критично: когда деньги заканчиваются,
// важна работа, которая вообще успеет.
func TestRouteTightBudgetDowngradesDemanding(t *testing.T) {
	opts := routeOpts()
	opts.Budget = Budget{Spent: 95, Limit: 100}
	v := RouteModel(TypeCoder, opts)
	if !v.Downgraded {
		t.Fatalf("на пределе бюджета роль не понижена: %s", v.Why)
	}
	if !strings.Contains(v.Why, "бюджет") {
		t.Errorf("причина не названа бюджетом: %s", v.Why)
	}
}

// TestRouteTightBudgetAtSeventyPercent — порог включается на 70%, а не в последний
// момент: оставшиеся 30% обычно уходят на два-три хода главного агента.
func TestRouteTightBudgetAtSeventyPercent(t *testing.T) {
	opts := routeOpts()
	opts.Budget = Budget{Spent: 69, Limit: 100}
	if v := RouteModel(TypeCoder, opts); v.Downgraded {
		t.Errorf("на 69%% роль понижена: %s", v.Why)
	}
	opts.Budget = Budget{Spent: 70, Limit: 100}
	if v := RouteModel(TypeCoder, opts); !v.Downgraded {
		t.Errorf("на 70%% роль не понижена: %s", v.Why)
	}
}

// TestRouteNeverPicksUnavailableModel — модель из воздуха не выбирается: её нет
// у провайдера, и субагент получил бы отказ вместо отчёта.
func TestRouteNeverPicksUnavailableModel(t *testing.T) {
	opts := routeOpts()
	opts.Available = []string{"claude-opus-4"} // дешёвых нет в списке
	v := RouteModel(TypeExplorer, opts)
	if v.Model != opts.Current {
		t.Errorf("выбрана модель не из списка провайдера: %q", v.Model)
	}
}

// TestRouteKeepsModelWhenPriceUnknown — с неизвестной ценой не гадаем: сравнивать
// не с чем, а догадка здесь стоит денег.
func TestRouteKeepsModelWhenPriceUnknown(t *testing.T) {
	opts := routeOpts()
	opts.Current = "какая-то-выдуманная-модель"
	opts.Available = []string{"claude-3-5-haiku"}
	v := RouteModel(TypeExplorer, opts)
	if v.Model != opts.Current {
		t.Errorf("модель заменена при неизвестной цене: %q", v.Model)
	}
	if !strings.Contains(v.Why, "неизвестна") {
		t.Errorf("причина не названа: %s", v.Why)
	}
}

// TestRouteKeepsModelWhenCheaperNotCheaper — если «дешёвая» модель не дешевле,
// менять нечего. Сюда попадают и равные по цене модели.
func TestRouteKeepsModelWhenCheaperNotCheaper(t *testing.T) {
	opts := routeOpts()
	opts.Current = "claude-3-5-haiku" // уже самая дешёвая
	v := RouteModel(TypeExplorer, opts)
	if v.Model != opts.Current {
		t.Errorf("выбрана не более дешёвая модель: %q (%s)", v.Model, v.Why)
	}
}

// TestRouteDeterministic — один и тот же вход обязан давать один и тот же
// ответ: иначе один проект считался бы по-разному при каждом запуске.
func TestRouteDeterministic(t *testing.T) {
	opts := routeOpts()
	opts.Budget = Budget{Spent: 95, Limit: 100}
	first := RouteModel(TypeFrontend, opts)
	for i := 0; i < 20; i++ {
		if got := RouteModel(TypeFrontend, opts); got.Model != first.Model {
			t.Fatalf("выбор нестабилен: %q против %q", got.Model, first.Model)
		}
	}
}

// TestRouteEmptyCurrentModel — пустая модель означает «как у главного», и это
// не то же самое, что «дешёвая»: подменять тут нечего.
func TestRouteEmptyCurrentModel(t *testing.T) {
	opts := routeOpts()
	opts.Current = ""
	opts.Budget = Budget{Spent: 99, Limit: 100}
	v := RouteModel(TypeExplorer, opts)
	if v.Model != "" {
		t.Errorf("подставлена модель вместо текущей: %q", v.Model)
	}
}

// TestJudgeBudgetThresholds — состояния «жмёт/предел» считаются на границах.
func TestJudgeBudgetThresholds(t *testing.T) {
	cases := []struct {
		spent, limit int
		tight        bool
		critical     bool
	}{
		{0, 100, false, false},
		{69, 100, false, false},
		{70, 100, true, false},
		{89, 100, true, false},
		{90, 100, true, true},
		{120, 100, true, true},
	}
	for _, c := range cases {
		v := JudgeBudget(Budget{Spent: c.spent, Limit: c.limit})
		if v.Tight != c.tight || v.Critical != c.critical {
			t.Errorf("%d%%: tight=%v critical=%v, ожидалось %v/%v",
				v.UsedPct, v.Tight, v.Critical, c.tight, c.critical)
		}
		if v.Left < 0 {
			t.Errorf("%d%%: остаток отрицательный (%d)", v.UsedPct, v.Left)
		}
	}
}

// TestJudgeBudgetWithoutLimit — без потолка бюджета решения не принимаются:
// это отсутствие бюджета, а не бесконечный бюджет.
func TestJudgeBudgetWithoutLimit(t *testing.T) {
	v := JudgeBudget(Budget{Spent: 10_000_000, Limit: 0})
	if v.Tight || v.Critical || v.UsedPct != 0 {
		t.Errorf("без потолка решение принято: %+v", v)
	}
	if v.String() != "" {
		t.Errorf("без потолка бюджет что-то сообщает: %q", v.String())
	}
}

// TestCostClassCoversEveryType — новая роль не должна молча попасть в
// CostNormal: явное перечисление классов — это и защита от сюрпризов.
func TestCostClassCoversEveryType(t *testing.T) {
	want := map[Type]CostClass{
		TypeExplorer:   CostCheap,
		TypeDocs:       CostCheap,
		TypeGeneral:    CostNormal,
		TypePlanner:    CostNormal,
		TypeResearcher: CostNormal,
		TypeCustom:     CostNormal,
		TypeReviewer:   CostDemanding,
		TypeCoder:      CostDemanding,
		TypeTester:     CostDemanding,
		TypeFrontend:   CostDemanding,
	}
	for _, typ := range Types {
		if got := CostClassOf(typ); got != want[typ] {
			t.Errorf("%s: класс %d (%s), ожидался %s", typ, got, got.Label(), want[typ].Label())
		}
	}
}

// TestTurnsCutOnlyAtLimit — ходы урезаются только на самом пределе: потеря хода
// бьёт по качеству сильнее смены модели.
func TestTurnsCutOnlyAtLimit(t *testing.T) {
	opts := routeOpts()
	opts.Budget = Budget{Spent: 50, Limit: 100}
	if got := TurnsForBudget(12, opts); got != 12 {
		t.Errorf("при здоровом бюджете ходы урезаны: %d", got)
	}
	opts.Budget = Budget{Spent: 75, Limit: 100}
	if got := TurnsForBudget(12, opts); got != 9 {
		t.Errorf("при 75%% ожидалось 9 ходов, получено %d", got)
	}
	opts.Budget = Budget{Spent: 95, Limit: 100}
	if got := TurnsForBudget(12, opts); got != 6 {
		t.Errorf("при 95%% ожидалось 6 ходов, получено %d", got)
	}
}

// TestTurnsNeverBelowFloor — три хода это «прочитать и не успеть сдать отчёт»:
// результата нет, а деньги потрачены.
func TestTurnsNeverBelowFloor(t *testing.T) {
	opts := routeOpts()
	opts.Budget = Budget{Spent: 100, Limit: 100}
	if got := TurnsForBudget(5, opts); got < 4 {
		t.Errorf("ходов осталось %d — субагент не сдаст отчёт", got)
	}
}

// TestRouteCostShownInReason — в объяснении видна цена: без неё «почему он
// поехал на haiku» остаётся вопросом без ответа.
func TestRouteCostShownInReason(t *testing.T) {
	v := RouteModel(TypeExplorer, routeOpts())
	if !strings.Contains(v.Why, "$") {
		t.Errorf("в объяснении нет цены: %s", v.Why)
	}
}

// TestPriceSumOrdering — выбор идёт по сумме ввода и вывода, а не по вводу.
// Здесь это видно буквально: у a вход дешевле (0.1 против 1.0), но из-за
// пятикратного вывода она в 2.5 раза дороже b по деньгам. Сравнение только
// по вводу назвало бы дешёвой именно ту модель, которая на длинной работе
// съедает бюджет.
func TestPriceSumOrdering(t *testing.T) {
	a := core.Price{In: 0.1, Out: 5}   // сумма 5.1
	b := core.Price{In: 1.0, Out: 1.0} // сумма 2.0

	// По вводу a дешевле b — и именно поэтому проверка на ввод здесь и ломается.
	if a.In >= b.In {
		t.Fatalf("тест бессмыслен: ввод у a не дешевле (%v против %v)", a.In, b.In)
	}
	// А по деньгам наоборот.
	if !cheaper(b, a) {
		t.Errorf("b (сумма %v) не признана дешевле a (сумма %v)", b.In+b.Out, a.In+a.Out)
	}
	if cheaper(a, b) {
		t.Error("a посчитана дешевле b, хотя сумма у неё больше — сравнение идёт не по сумме")
	}
}
