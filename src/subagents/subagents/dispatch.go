package subagents

import (
	"fmt"
	"sort"
	"strings"
)

// ---------- Автовыбор роли (dispatcher) ----------
//
// Модель зовёт spawn_agent и почти всегда пишет type руками — причём неверно:
// «найди, где реализована авторизация» уходит в coder (который начнёт править
// файлы), а «посмотри, есть ли гонки» — в explorer (который не ищет баги, а
// составляет карту). Результат — субагент, который качественно делает не то.
//
// Выбор роли по одному только типу невозможен: одно и то же «проверь
// безопасность» — это и reviewer с чтением кода, и general с правками. Нужен
// ещё один сигнал — намерение, выраженное в тексте задачи. Dispatcher читает
// именно его: разбирает задачу на признаки (слово «исправь», «тест»,
// «скриншот», «документация»…) и выбирает роль по совокупности.
//
// Что это НЕ делает: не обращается к модели и не гадает. Решение
// детерминированное, объяснимое и одинаковое от запуска к запуску — иначе
// автовыбор был бы новым источником непредсказуемости.

// DispatchVerdict — решение диспетчера с объяснением.
type DispatchVerdict struct {
	// Type — выбранный тип субагента.
	Type Type
	// Score — насколько уверенно сработали признаки в тексте задачи.
	Score int
	// Signals — какие именно слова на что указывали, по убыванию веса.
	// Это не отладочный мусор: модель должна видеть, ПОЧЕМУ выбрана именно
	// эта роль, иначе автовыбор останется чёрным ящиком и его перестанут
	// доверять при неудаче.
	Signals []DispatchSignal
}

// DispatchSignal — одно сработавшее правило.
type DispatchSignal struct {
	Role   Type
	Word   string
	Weight int
}

// HasRoleMention — был ли указан тип явно.
//
// Отдельное поле, а не сравнение с "": главный агент может указать
// пользовательского агента из .gcli/agents/*.md, которого нет в списке
// типов. Считать это «не указал» нельзя — тогда dispatcher перезаписал бы
// выбор модели.
func (v DispatchVerdict) HasRoleMention() bool { return v.Score > 0 }

// Reason — краткое объяснение выбора для отчёта и подсказки модели.
func (v DispatchVerdict) Reason() string {
	if len(v.Signals) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "роль выбрана автоматически: %s (уверенность %d) — сработали признаки: ",
		v.Type, v.Score)
	parts := make([]string, 0, len(v.Signals))
	for _, s := range v.Signals {
		parts = append(parts, fmt.Sprintf("%s→%s", s.Word, s.Role))
	}
	b.WriteString(strings.Join(parts, ", "))
	return b.String()
}

// dispatchRule — признак в тексте задачи, указывающий на роль.
//
// Веса не равны: «тест» в любом контексте может означать «написать тест», а
// «исправь» почти всегда означает правку кода. Сильные признаки (вес 3)
// перевешивают слабые (вес 1), и потому «поправь баг и добавь тест» уходит в
// coder, а не в tester.
type dispatchRule struct {
	role   Type
	word   string
	weight int
}

// dispatchRules — словарь признаков.
//
// Список намеренно короткий и без регулярных выражений: регулярки требуют
// настройки под язык пользователя, а слова работают на любом.
// Нечитаемое слово — просто не сработает, и dispatcher откатится к general,
// а не к неверной роли.
var dispatchRules = []dispatchRule{
	// Реализация.
	{TypeCoder, "исправь", 3},
	{TypeCoder, "исправить", 3},
	{TypeCoder, "поправь", 3},
	{TypeCoder, "перепиши", 3},
	{TypeCoder, "реализуй", 3},
	{TypeCoder, "реализовать", 3},
	{TypeCoder, "добавь функцию", 3},
	{TypeCoder, "напиши код", 3},
	{TypeCoder, "сделай правку", 3},
	{TypeCoder, "почини", 3},
	{TypeCoder, "оптимизируй", 2},
	{TypeCoder, "перенеси", 2},
	{TypeCoder, "вынеси в", 2},

	// Тесты.
	{TypeTester, "тест", 3},
	{TypeTester, "тесты", 3},
	{TypeTester, "покрытие", 3},
	{TypeTester, "покрыть", 3},
	{TypeTester, "прогоняй тесты", 3},
	{TypeTester, "падает", 2},
	{TypeTester, "падающий тест", 3},
	{TypeTester, "юнит-тест", 3},

	// Ревью: умысел «найти дефекты», а не «разобраться».
	{TypeReviewer, "найди баги", 3},
	{TypeReviewer, "найди баг", 3},
	{TypeReviewer, "баги", 2},
	{TypeReviewer, "уязвимости", 3},
	{TypeReviewer, "уязвимость", 3},
	{TypeReviewer, "безопасность", 2},
	{TypeReviewer, "гонки", 3},
	{TypeReviewer, "ревью", 3},
	{TypeReviewer, "проверь качество", 2},
	{TypeReviewer, "критик", 3},
	{TypeReviewer, "утечка", 2},
	{TypeReviewer, "небезопасн", 3},

	// Планирование.
	{TypePlanner, "план", 3},
	{TypePlanner, "планирование", 3},
	{TypePlanner, "спроектируй", 3},
	{TypePlanner, "архитектур", 3},
	{TypePlanner, "разбей на шаги", 3},
	{TypePlanner, "декомпоз", 3},
	{TypePlanner, "оцени", 2},
	{TypePlanner, "сравни варианты", 3},

	// Вёрстка.
	{TypeFrontend, "вёрстк", 3},
	{TypeFrontend, "верстк", 3},
	{TypeFrontend, "ui", 2},
	{TypeFrontend, "интерфейс", 2},
	{TypeFrontend, "css", 3},
	{TypeFrontend, "стили", 2},
	{TypeFrontend, "дизайн", 2},
	{TypeFrontend, "скриншот", 3},
	{TypeFrontend, "кнопк", 2},
	{TypeFrontend, "адаптив", 3},

	// Исследование кода.
	{TypeExplorer, "где находится", 3},
	{TypeExplorer, "найди файл", 3},
	{TypeExplorer, "найди функцию", 3},
	{TypeExplorer, "карта проекта", 3},
	{TypeExplorer, "изучи код", 3},
	{TypeExplorer, "разберись", 2},
	{TypeExplorer, "как устроен", 3},
	{TypeExplorer, "структура", 2},
	{TypeExplorer, "кто вызывает", 3},

	// Сеть.
	{TypeResearcher, "интернет", 3},
	{TypeResearcher, "документаци", 2},
	{TypeResearcher, "найди в сети", 3},
	{TypeResearcher, "свежая версия", 3},
	{TypeResearcher, "api", 2},
	{TypeResearcher, "выпустили ли", 3},
	{TypeResearcher, "сравнение библиотек", 3},

	// Документация проекта.
	{TypeDocs, "напиши документацию", 3},
	{TypeDocs, "readme", 3},
	{TypeDocs, "гайд", 2},
	{TypeDocs, "changelog", 3},
	{TypeDocs, "документируй", 3},
}

// fallbackType — роль по умолчанию, когда признаков нет.
//
// general, а не explorer: универсал имеет все инструменты и не обрезан
// промптом под одну задачу. Назначение explorer'а по умолчанию означало бы
// запрет на запись для любой задачи без глагола — и тихий отказ там, где
// правка была нужна.
const fallbackType = TypeGeneral

// minDispatchScore — ниже этого вердикт неуверенный и роль не навязывается.
//
// Один слабый признак («ui» в «изучи ui-пакет») не должен переопределять
// выбор модели. Порог 3 означает: нужно либо один сильный признак (вес 3),
// либо несколько слабых.
const minDispatchScore = 3

// Dispatch — выбрать роль по тексту задачи.
//
// Возвращает TypeCustom, если признаков нет и воля модели не выражена: вызывающий
// код решает, что делать с таким результатом (сейчас — подставлять general).
// Разделение сделано намеренно, чтобы правило подстановки жило в одном месте
// (см. ResolveType), а не расползлось по вызовам.
func Dispatch(task string) DispatchVerdict {
	v := DispatchVerdict{Type: TypeCustom}
	lower := strings.ToLower(task)
	if strings.TrimSpace(lower) == "" {
		return v
	}

	type agg struct {
		score  int
		signal DispatchSignal
	}
	byRole := map[Type]*agg{}
	var order []Type
	for _, r := range dispatchRules {
		if !strings.Contains(lower, r.word) {
			continue
		}
		a := byRole[r.role]
		if a == nil {
			a = &agg{signal: DispatchSignal{Role: r.role, Word: r.word}}
			byRole[r.role] = a
			order = append(order, r.role)
		}
		// Первое совпадение даёт самое сильное слово для показа; сумма весов
		// при этом растёт по всем сработавшим признакам.
		if r.weight > a.signal.Weight {
			a.signal = DispatchSignal{Role: r.role, Word: r.word, Weight: r.weight}
		}
		a.score += r.weight
	}

	// Сортировка по сумме весов; при равенстве — по имени роли, иначе выбор
	// зависел бы от порядка слов в правилах и менялся при их правке.
	type scored struct {
		role   Type
		score  int
		signal DispatchSignal
	}
	list := make([]scored, 0, len(order))
	for _, r := range order {
		a := byRole[r]
		list = append(list, scored{role: r, score: a.score, signal: a.signal})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].role < list[j].role
	})

	var all []DispatchSignal
	for _, s := range list {
		all = append(all, s.signal)
	}
	v.Signals = all
	if len(list) == 0 || list[0].score < minDispatchScore {
		return v // неуверенно: роль не навязывается
	}
	v.Type = list[0].role
	v.Score = list[0].score
	return v
}

// ResolveType — определить тип субагента с учётом автовыбора.
//
// Аргументы повторяют то, что реально знает вызывающий: явный тип от модели
// (пусто — не указан), текст задачи и список имён пользовательских агентов.
// Имя пользовательского агента проверяется ДО разбора: иначе имя вроде
// «архитектор» превратится в синоним planner и перебьёт файл .gcli/agents.
func ResolveType(explicit, task string, customNames []string) (Type, DispatchVerdict) {
	e := strings.TrimSpace(explicit)
	if e != "" {
		// Пользовательский агент важнее любого разбора: это конкретный
		// промпт из файла, а не абстрактный тип.
		for _, n := range customNames {
			if strings.EqualFold(n, e) {
				return TypeCustom, DispatchVerdict{}
			}
		}
		t, _ := ParseType(e)
		return t, DispatchVerdict{}
	}
	v := Dispatch(task)
	if v.Score >= minDispatchScore {
		return v.Type, v
	}
	return fallbackType, v
}
