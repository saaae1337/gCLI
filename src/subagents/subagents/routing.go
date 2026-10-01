package subagents

import (
	"fmt"
	"sort"
	"strings"

	"gcli/core"
)

// ---------- Бюджетная маршрутизация моделей ----------
//
// Симптом, который это закрывает. Пользователь настроил субагентов на дорогую
// модель («у меня ключ, качество важнее») — и платит её цену за каждую мелочь:
// за карту проекта, за «переименуй переменную», за «напиши раздел README».
// Стоимость субагента не зависит от того, насколько сложна его задача: один и
// тот же код с ролью explorer и ролью coder стоит одинаково.
//
// Что здесь НЕ делается: модель никогда не выбирается «на глаз», а цены не
// выдумываются. Всё решение детерминировано, опирается на таблицу цен
// core.ModelPrice и объяснимо в одну строку — иначе пользователь не смог бы
// понять, почему его субагент вдруг поехал на другой модели.
//
// Модель понижается, но никогда не повышается: незаметно увести субагента на
// более дорогую модель — значит незаметно увеличить счёт.

// CostClass — насколько роль чувствительна к качеству модели.
//
// Классификация по существу работы, а не по названию роли: explorer читает
// куски кода и перечисляет факты, где сильная модель не даёт преимущества
// перед аккуратной дешёвой; coder и frontend правят код, который потом
// проверяется компилятором и глазами, и там дешёвая модель платит дважды —
// за брак и за переделывание.
type CostClass int

const (
	// CostCheap — роль обходится без топовой модели.
	CostCheap CostClass = iota
	// CostNormal — средний класс: качество заметно, но не критично.
	CostNormal
	// CostDemanding — качество модели напрямую определяет результат.
	CostDemanding
)

// CostClassOf — во сколько обходится роль.
//
// Порядок Types здесь неслучаен, но опираться на него нельзя: список
// пополняется, и молчаливый сдвиг классификации изменил бы маршрутизацию для
// всех ролей разом. Поэтому роли перечислены явно — лишняя строка стоит
// дешевле, чем тихая смена поведения при правке соседней роли.
func CostClassOf(t Type) CostClass {
	switch t {
	case TypeExplorer, TypeDocs:
		return CostCheap
	case TypeGeneral, TypePlanner, TypeResearcher, TypeCustom:
		return CostNormal
	case TypeReviewer, TypeCoder, TypeTester, TypeFrontend:
		return CostDemanding
	}
	return CostNormal
}

// Label — человекочитаемое имя класса.
func (c CostClass) Label() string {
	switch c {
	case CostCheap:
		return "простая"
	case CostDemanding:
		return "требовательная"
	}
	return "средняя"
}

// Budget — состояние бюджета токенов.
type Budget struct {
	// Spent — потрачено токенов за сессию (главный агент + субагенты).
	Spent int
	// Limit — потолок сессии в токенах. Ноль — бюджет не задан: тогда решения
	// по нему не принимаются вовсе, и это не «бюджет бесконечный», а
	// отсутствие бюджета. Подставлять тут потолок по умолчанию нельзя —
	// тогда понижение моделей включалось бы у всех молча.
	Limit int
}

// JudgeBudget — оценить, насколько близко бюджет к потолку.
type BudgetVerdict struct {
	Left     int
	UsedPct  int
	Tight    bool
	Critical bool
}

// judgeBudget — пороги, при которых маршрутизация включается.
//
// 70% и 90%, а не «почти конец»: оставшиеся 30% сессии — это обычно два-три
// хода главного агента вместе с отчётами субагентов. Менять модель нужно
// ДО того, как деньги кончатся, а не когда менять уже поздно.
const (
	budgetTightPct    = 70
	budgetCriticalPct = 90
)

// JudgeBudget — перевести расход в решение: жмёт ли бюджет.
func JudgeBudget(b Budget) BudgetVerdict {
	v := BudgetVerdict{Left: b.Limit - b.Spent}
	if b.Limit <= 0 {
		// Потолка нет — судить не о чем.
		return v
	}
	if v.Left < 0 {
		v.Left = 0
	}
	v.UsedPct = core.Pct(b.Spent, b.Limit)
	v.Tight = v.UsedPct >= budgetTightPct
	v.Critical = v.UsedPct >= budgetCriticalPct
	return v
}

// String — короткое состояние бюджета для подсказки модели.
func (v BudgetVerdict) String() string {
	switch {
	case v.Critical:
		return fmt.Sprintf("бюджет сессии израсходован на %d%% (осталось ~%s токенов)",
			v.UsedPct, core.Kfmt(v.Left))
	case v.Tight:
		return fmt.Sprintf("бюджет сессии израсходован на %d%% (осталось ~%s токенов)",
			v.UsedPct, core.Kfmt(v.Left))
	}
	return ""
}

// RouteOptions — что известно о запуске субагента.
type RouteOptions struct {
	// Explicit — модель, которую назвала модель. Её не трогают никогда: явное
	// указание человека — это и есть управление.
	Explicit string
	// Current — модель, на которой субагент поедет без маршрутизации
	// (SubModel из конфига либо модель главного агента).
	Current string
	// ProviderID — провайдер, для нужны цены и список его моделей.
	ProviderID string
	// Available — модели, доступные у провайдера. Пусто — выбирать не из чего.
	Available []string
	// Budget — текущее состояние бюджета.
	Budget Budget
	// Enabled — разрешена ли маршрутизация вообще (по умолчанию выключена:
	// молча менять модель субагента нельзя).
	Enabled bool
}

// RouteVerdict — решение маршрутизатора с объяснением.
type RouteVerdict struct {
	// Model — модель для запуска; пусто означает «оставить текущую».
	Model string
	// Why — почему так. Показывается модели и в журнале запуска: невидимое
	// решение объяснить невозможно, и пользователь будет считать глюком.
	Why string
	// Downgraded — модель понижена (для журнала и метрик).
	Downgraded bool
}

// RouteModel — выбрать модель субагента по роли и бюджету.
//
// Порядок проверок задаёт приоритеты, и он не случаен:
//
//  1. Явное указание модели — стоп. Человек сказал, куда ехать.
//  2. Маршрутизация выключена — стоп. Молчаливый выбор модели дороже, чем
//     её отсутствие: пользователь узнаёт о подмене по счёту.
//  3. Классическая роль: простая работа едет на дешёвой модели, даже когда
//     бюджет в порядке.
//  4. Жёсткий бюджет: жмёт — понижаем ВСЁ, включая требовательные роли.
//     Порядок обратный обычному намеренно: когда деньги заканчиваются, важна
//     не лучшая работа, а работа, которая вообще успеет.
//
// Чего не делается: модель не повышается никогда, и не выбирается модель с
// неизвестной ценой — сравнивать не с чем, а догадка здесь стоит денег.
func RouteModel(role Type, opts RouteOptions) RouteVerdict {
	cur := strings.TrimSpace(opts.Current)
	if e := strings.TrimSpace(opts.Explicit); e != "" {
		return RouteVerdict{Model: cur, Why: "модель указана явно — не меняю"}
	}
	if !opts.Enabled {
		return RouteVerdict{Model: cur, Why: "маршрутизация выключена"}
	}
	if cur == "" {
		// Нечего понижать: пустая модель — это «как у главного агента».
		return RouteVerdict{Model: "", Why: "модель не задана — едет как у главного агента"}
	}
	bv := JudgeBudget(opts.Budget)
	cheap := cheapest(opts.ProviderID, opts.Available)
	if cheap.model == "" {
		return RouteVerdict{Model: cur, Why: "нет более дешёвой модели у провайдера — оставляю " + cur}
	}
	curPrice, ok := core.ModelPrice(opts.ProviderID, cur)
	if !ok {
		return RouteVerdict{Model: cur, Why: "цена модели " + cur + " неизвестна — не гадаю"}
	}

	wantCheap := CostClassOf(role) == CostCheap || bv.Critical || bv.Tight
	if !wantCheap {
		return RouteVerdict{Model: cur, Why: fmt.Sprintf("роль «%s» требовательна, бюджет в порядке", role)}
	}
	if !cheaper(cheap.price, curPrice) {
		return RouteVerdict{Model: cur, Why: fmt.Sprintf("%s не дешевле %s — оставляю", cheap.model, cur)}
	}

	why := fmt.Sprintf("роль «%s» простая, модель %s (%s) дороже %s",
		role, cur, core.FormatUSD(sampleCost(curPrice)), cheap.model)
	if bv.Critical {
		why += "; бюджет на пределе (" + bv.String() + ")"
	} else if bv.Tight {
		why += "; бюджет жмёт (" + bv.String() + ")"
	}
	return RouteVerdict{Model: cheap.model, Why: why, Downgraded: true}
}

// TurnsForBudget — сколько ходов дать субагенту при текущем бюджете.
//
// Урезание ходов бьёт по качеству сильнее, чем смена модели, поэтому
// применяется только на самом пределе и не опускается ниже четырёх: три хода
// это «прочитать, понять, не успеть сдать отчёт», то есть выброшенные деньги.
// Порядок уменьшения детерминированный: тот же расход даёт тот же результат.
func TurnsForBudget(base int, opts RouteOptions) int {
	if base <= 0 {
		base = 12
	}
	if !opts.Enabled {
		return base
	}
	bv := JudgeBudget(opts.Budget)
	switch {
	case bv.Critical:
		return maxInt(4, base/2)
	case bv.Tight:
		return maxInt(4, base*3/4)
	}
	return base
}

// priced — модель вместе с ценой: сортировать надо по цене, а не по имени.
type priced struct {
	model string
	price core.Price
	score float64
}

// cheapest — самая дешёвая модель из доступных.
//
// Только модели с известной ценой и только те, что реально есть у провайдера:
// выбрать красивую модель, которой нет в списке, значит отправить субагента на
// несуществующий endpoint и получить отказ вместо отчёта.
//
// Сортировка по (цена, имя) — выбор обязан быть одинаковым от запуска к
// запуску, иначе один и тот же проект давал бы разный счёт.
func cheapest(providerID string, available []string) priced {
	var out []priced
	for _, m := range available {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		p, ok := core.ModelPrice(providerID, m)
		if !ok {
			continue
		}
		out = append(out, priced{model: m, price: p, score: p.In + p.Out})
	}
	if len(out) == 0 {
		return priced{}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score < out[j].score
		}
		return out[i].model < out[j].model
	})
	return out[0]
}

// cheaper — дешевле ли a по цене за условные миллион токенов на каждую сторону.
//
// Сравнение по сумме ввода и вывода, а не по вводу: у всех моделей вывод
// дороже входа примерно в пять раз, и модель с дешёвым входом и дорогим
// выводом на длинной работе выходит дороже. Для выбора «проще не дороже» такая
// грубость безопасна, а точный прогноз расхода здесь всё равно был бы гаданием.
func cheaper(a, b core.Price) bool { return a.In+a.Out < b.In+b.Out }

// sampleCost — стоимость условного миллиона токенов на каждую сторону.
func sampleCost(p core.Price) float64 { return p.In + p.Out }
