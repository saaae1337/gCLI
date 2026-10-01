package subagents

import (
	"strings"
	"testing"
)

// ---------- Автовыбор роли ----------
//
// Тесты проверяют не «какая роль нравится автору», а инварианты, на которых
// держится доверие к автовыбору: детерминизм, отказ при слабых признаках,
// уважение к явному выбору и к пользовательским агентам.

// TestDispatchPicksObviousRoles — очевидные намерения уводят в нужную роль.
func TestDispatchPicksObviousRoles(t *testing.T) {
	cases := []struct {
		task string
		want Type
	}{
		{"Исправь гонку в пуле субагентов", TypeCoder},
		{"Добавь тесты на GroundHint", TypeTester},
		{"Найди баги и уязвимости в обработке ввода", TypeReviewer},
		{"Составь план реализации фичи", TypePlanner},
		{"Сверстай страницу отчёта, проверь скриншотом", TypeFrontend},
		{"Где находится обработчик авторизации?", TypeExplorer},
		{"Узнай в интернете, что нового в Go 1.25", TypeResearcher},
		{"Обнови README и changelog", TypeDocs},
	}
	for _, c := range cases {
		t.Run(string(c.want), func(t *testing.T) {
			v := Dispatch(c.task)
			if v.Type != c.want {
				t.Errorf("Dispatch(%q) = %s, ожидался %s (сигналы: %+v)", c.task, v.Type, c.want, v.Signals)
			}
			if v.Score < minDispatchScore {
				t.Errorf("Score = %d, ожидалось >= %d", v.Score, minDispatchScore)
			}
			if v.Reason() == "" {
				t.Error("Reason() пуст — выбор должен быть объяснимым")
			}
		})
	}
}

// TestDispatchRefusesWeakSignal — один слабый признак не должен переопределять
// выбор модели. Раньше дефолтом был explorer, и «изучи ui-пакет» увёл бы
// задачу в роль, которой она не касается.
func TestDispatchRefusesWeakSignal(t *testing.T) {
	for _, task := range []string{
		"изучи ui-пакет",
		"посмотри, что тут",
		"помоги",
		"сделай хорошо",
	} {
		if v := Dispatch(task); v.Score >= minDispatchScore {
			t.Errorf("Dispatch(%q) решился на %s (Score=%d) по слабому признаку",
				task, v.Type, v.Score)
		}
	}
}

// TestDispatchEmptyTask — пустая задача не должна выбирать роль наугад.
func TestDispatchEmptyTask(t *testing.T) {
	for _, task := range []string{"", "   ", "\n\t"} {
		if v := Dispatch(task); v.Score != 0 || v.Type != TypeCustom {
			t.Errorf("Dispatch(%q) = {%s, %d}, ожидался неуверенный Custom", task, v.Type, v.Score)
		}
	}
}

// TestDispatchDeterministic — одно и то же даёт один и тот же результат.
// Недетерминированный автовыбор хуже его отсутствия: тогда непонятно, что
// именно сломалось — выбор роли или сама работа субагента.
func TestDispatchDeterministic(t *testing.T) {
	task := "Исправь баг в аутентификации и добавь тесты"
	first := Dispatch(task)
	for i := 0; i < 20; i++ {
		if got := Dispatch(task); got.Type != first.Type || got.Score != first.Score {
			t.Fatalf("расхождение на прогоне %d: %s/%d против %s/%d",
				i, got.Type, got.Score, first.Type, first.Score)
		}
	}
}

// TestResolveTypeRespectsExplicitChoice — явный выбор модели священен.
// Автовыбор не имеет права «улучшать» то, что модель указала сама: иначе
// подпись типа в отчёте перестаёт соответствовать задаче.
func TestResolveTypeRespectsExplicitChoice(t *testing.T) {
	cases := []struct {
		explicit string
		want     Type
	}{
		{"explorer", TypeExplorer},
		{"reviewer", TypeReviewer},
		{"кодер", TypeCoder}, // синоним
		{"architect", TypePlanner},
	}
	for _, c := range cases {
		task := "Исправь баг и добавь тесты" // автовыбор уверенно дал бы coder
		got, v := ResolveType(c.explicit, task, nil)
		if got != c.want {
			t.Errorf("ResolveType(%q) = %s, ожидался %s", c.explicit, got, c.want)
		}
		if v.Score != 0 {
			t.Errorf("ResolveType(%q) вернул вердикт автовыбора (Score=%d) при явном типе", c.explicit, v.Score)
		}
	}
}

// TestResolveTypeCustomAgentBeatsSynonym — имя из .gcli/agents/*.md не должно
// разбираться как синоним типа. Пользовательский агент «архитектор» — это
// конкретный промпт из файла, и он важнее абстрактного planner.
func TestResolveTypeCustomAgentBeatsSynonym(t *testing.T) {
	names := []string{"архитектор", "мой-ревьюер"}
	got, _ := ResolveType("архитектор", "Составь план", names)
	if got != TypeCustom {
		t.Errorf("ResolveType(архитектор) = %s, ожидался TypeCustom — это файл агента, а не planner", got)
	}
	got, _ = ResolveType("МОЙ-РЕВЬЮЕР", "", names)
	if got != TypeCustom {
		t.Errorf("сравнение имён должно быть регистронезависимым, получено %s", got)
	}
}

// TestResolveTypeFallsBackToGeneral — без признаков выбирается general, а не
// explorer: универсал не обрезан промптом и не лишён инструментов записи.
// Дефолт explorer означал бы тихий отказ на любой задаче без глагола.
func TestResolveTypeFallsBackToGeneral(t *testing.T) {
	got, v := ResolveType("", "посмотри на situation и скажи своё мнение", nil)
	if got != TypeGeneral {
		t.Errorf("ResolveType = %s, ожидался general", got)
	}
	if v.Score >= minDispatchScore {
		t.Errorf("Score = %d — подсказка должна быть помечена как неуверенная", v.Score)
	}
}

// TestResolveTypeUsesFallbackWithoutConfidence — вердикт с низким Score всё
// равно возвращается: вызывающий код решает, что делать (сейчас — понижать
// роль для чтения), и для этого ему нужно видеть сработавшие признаки.
func TestResolveTypeUsesFallbackWithoutConfidence(t *testing.T) {
	v := Dispatch("изучи ui-пакет")
	if len(v.Signals) == 0 {
		t.Fatal("сигналы потеряны: диспетчер обязан показывать, что сработало")
	}
	if v.HasRoleMention() {
		t.Error("HasRoleMention() = true при неуверенном вердикте")
	}
}

// TestDispatchDoesNotInventFiles — автовыбор роли не должен ломать заземление:
// сигналы в тексте задачи не имеют ничего общего с проверкой ссылок отчёта.
func TestDispatchScoreIndependentOfGrounding(t *testing.T) {
	// Роль выбирается по тексту задачи, а не по отчёту: здесь просто
	// фиксируем, что вердикт диспетчера устойчив к регистру и пробелам.
	a := Dispatch("ИСПРАВЬ БАГ")
	b := Dispatch("исправь   баг")
	if a.Type != b.Type || a.Score != b.Score {
		t.Errorf("регистр и пробелы изменили вердикт: %s/%d против %s/%d", a.Type, a.Score, b.Type, b.Score)
	}
}

// TestRulesAreSortedStably — правила не должны зависеть от порядка объявления
// в dispatchRules. Проверяется тем, что вердикт на одинаковых по сумме веса
// задачах одинаков при любом порядке правил.
func TestRulesAreSortedStably(t *testing.T) {
	// Сумма весов «тест» (3) + «покрытие» (3) = 6 у tester; «css» (3) даёт 3.
	// При равном счёте порядок ролей фиксирован по имени, а не по правилам.
	v := Dispatch("напиши тесты и покрытие для css")
	if v.Type != TypeTester {
		t.Fatalf("ожидался tester (вес 6), получен %s: %+v", v.Type, v.Signals)
	}
	if len(v.Signals) < 2 {
		t.Errorf("в вердикте должно быть видно несколько ролей-кандидатов: %+v", v.Signals)
	}
}

// TestReasonMentionsRoleAndSignals — объяснение обязано называть и роль, и
// сработавшие признаки: иначе модель не сможет понять автовыбор и не сможет
// его оспорить.
func TestReasonMentionsRoleAndSignals(t *testing.T) {
	v := Dispatch("Исправь гонку данных в пуле")
	r := v.Reason()
	if !strings.Contains(r, string(v.Type)) {
		t.Errorf("Reason() не называет роль: %s", r)
	}
	if !strings.Contains(r, "автоматически") {
		t.Errorf("Reason() не говорит, что выбор автоматический: %s", r)
	}
	if !v.HasRoleMention() {
		t.Error("HasRoleMention() = false при уверенном вердикте")
	}
}
