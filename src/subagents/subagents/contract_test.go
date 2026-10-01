package subagents

import (
	"strings"
	"testing"
)

// ---------- Контракт секций ----------

func TestRequiredSectionsMatchPrompt(t *testing.T) {
	// Контракт, которого нет в промпте, — враньё: модель его не выполнит,
	// и валидатор будет штрафовать нормальные отчёты.
	for _, ty := range Types {
		sec := RequiredSections(ty)
		if len(sec) == 0 {
			if HasContract(ty) {
				t.Errorf("%s: HasContract=true, но RequiredSections пуст", ty)
			}
			continue
		}
		prompt := ty.Prompt(PromptContext{WorkDir: "."})
		for _, s := range sec {
			if !strings.Contains(prompt, "## "+s) {
				t.Errorf("%s: секция %q не запрошена в промпте", ty, s)
			}
		}
	}
}

func TestCheckSectionsDetectsMissing(t *testing.T) {
	full := "## Найдено\n- pool.go:10 — вот\nКак это работает\n- вызов по цепочке"
	ch := CheckSections(full, TypeExplorer)
	if len(ch.Missing) != 1 || ch.Missing[0] != "Вывод" {
		t.Fatalf("пропущено %+v, ждали [Вывод]", ch.Missing)
	}
	if ch.OK() {
		t.Error("OK() = true при отсутствующей секции")
	}
	if !strings.Contains(ch.Text(), "Вывод") {
		t.Errorf("Text() без названия секции: %q", ch.Text())
	}
}

func TestCheckSectionsAcceptsAnnotated(t *testing.T) {
	// Модели любят уточнения в заголовке — это не нарушение.
	full := "## Найдено (что именно)\n- pool.go:10\n## Вывод по задаче\n- всё ясно"
	if ch := CheckSections(full, TypeExplorer); !ch.OK() {
		t.Fatalf("уточнения в заголовках приняты за отсутствие секций: %+v", ch.Missing)
	}
}

func TestCheckSectionsNotFooledByProse(t *testing.T) {
	// «Проверка не выполнялась» — это не секция «## Проверка».
	full := "## Сделано\n- поправил баг\nПроверка не выполнялась, тесты не гонял."
	ch := CheckSections(full, TypeCoder)
	if len(ch.Missing) != 1 || ch.Missing[0] != "Проверка" {
		t.Fatalf("проза засчитана как секция: %+v", ch.Missing)
	}
}

func TestCheckSectionsNoContractIsOK(t *testing.T) {
	// У general нет предписанного формата: требовать секции незачем.
	if ch := CheckSections("просто текст без заголовков", TypeGeneral); !ch.OK() {
		t.Fatalf("у general нет контракта: %+v", ch.Missing)
	}
}

func TestSectionKeyForms(t *testing.T) {
	cases := []struct {
		line string
		key  string
		ok   bool
	}{
		{"## Найдено", "найдено", true},
		{"### Сделано:", "сделано", true},
		{"  ## Факты (текущее состояние)  ", "факты", true},
		{"**Итог**", "", false},
		{"- пункт списка", "", false},
		{"## ", "", false},
	}
	for _, c := range cases {
		k, ok := sectionKey(c.line)
		if ok != c.ok || k != c.key {
			t.Errorf("sectionKey(%q) = %q,%v; ждали %q,%v", c.line, k, ok, c.key, c.ok)
		}
	}
}

// ---------- Вердикт ----------

func TestJudgeReportOrder(t *testing.T) {
	spec := Spec{Type: TypeExplorer}
	// Форма проверяется первой: пустой отчёт не спасти секциями.
	if q := JudgeReport(spec, Outcome{Full: ""}); q != ReportEmpty {
		t.Errorf("пустой отчёт: %v, ждали ReportEmpty", q)
	}
	// Секции — второй шаг. Текст читаемый и структурный, но без заголовков.
	full := "- посмотрел код\n- вот что нашёл\n- вывод: всё в порядке"
	if q := JudgeReport(spec, Outcome{Full: full}); q != ReportNoSections {
		t.Errorf("без секций: %v, ждали ReportNoSections", q)
	}
	// Всё на месте — вердикт чистый.
	ok := "## Найдено\n- pool.go:10 — вот\n## Вывод\n- всё ясно"
	if q := JudgeReport(spec, Outcome{Full: ok}); q != ReportOK {
		t.Errorf("полный отчёт: %v, ждали ReportOK", q)
	}
}

func TestJudgeReportUnverified(t *testing.T) {
	full := "## Найдено\n- pool.go:10 — вот\n## Вывод\n- всё ясно"
	spec := Spec{Type: TypeExplorer}
	audit := &GroundingReport{Checked: 4, Supported: 1, FilesRead: 1,
		Phantoms: []string{"ghost.go"}}
	if q := JudgeReport(spec, Outcome{Full: full, Audit: audit}); q != ReportUnverified {
		t.Errorf("фантомы в отчёте: %v, ждали ReportUnverified", q)
	}
}

func TestNeedsRepairThresholds(t *testing.T) {
	cases := []struct {
		name string
		a    *GroundingReport
		want bool
	}{
		{"нет заземления", nil, false},
		{"ничего не читал", &GroundingReport{Checked: 3, Supported: 0}, false},
		{"нечего проверять", &GroundingReport{FilesRead: 2}, false},
		{"фантом", &GroundingReport{FilesRead: 2, Checked: 5, Supported: 5, Phantoms: []string{"g.go"}}, true},
		{"опора слабая", &GroundingReport{FilesRead: 2, Checked: 4, Supported: 1}, true},
		{"одна оговорка среди многих", &GroundingReport{FilesRead: 2, Checked: 20, Supported: 19}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NeedsRepair(c.a); got != c.want {
				t.Errorf("NeedsRepair = %v, ждали %v", got, c.want)
			}
		})
	}
}

func TestGroundHintIsAddressed(t *testing.T) {
	a := &GroundingReport{
		FilesRead:   1,
		Checked:     2,
		Supported:   0,
		Phantoms:    []string{"ghost.go"},
		Unsupported: []Claim{{Where: "pool.go:39", Path: "pool.go", Line: 39, Why: string(RiskLine)}},
	}
	h := GroundHint(a)
	for _, want := range []string{"pool.go:39", "ghost.go", "не подтвердился"} {
		if !strings.Contains(h, want) {
			t.Errorf("в GroundHint нет %q:\n%s", want, h)
		}
	}
}

func TestGroundHintCapped(t *testing.T) {
	var claims []Claim
	for i := 0; i < 40; i++ {
		claims = append(claims, Claim{Where: "f.go:" + itoa(i), Path: "f.go", Line: i})
	}
	h := GroundHint(&GroundingReport{FilesRead: 1, Checked: 40, Unsupported: claims})
	if n := strings.Count(h, "f.go:"); n > 13 {
		t.Errorf("список проблемных мест не обрезан: %d строк", n)
	}
}

// TestSortedProblemsStable — список мест для добивки не должен зависеть от
// порядка обхода. Problems() берёт Unsupported по порядку появления в
// отчёте, а добавляет фантомы в конце; повторный прогон того же отчёта
// обязан дать ту же строку подсказки.
func TestSortedProblemsStable(t *testing.T) {
	a := &GroundingReport{
		Unsupported: []Claim{{Where: "b.go:2"}, {Where: "a.go:1"}},
		Phantoms:    []string{"z.go"},
	}
	if got := sortedProblems(a); len(got) != 3 || got[0] != "a.go:1" || got[2] != "z.go" {
		t.Fatalf("порядок нестабилен: %+v", got)
	}
}

func TestGroundHintCleansCleanReport(t *testing.T) {
	if GroundHint(&GroundingReport{FilesRead: 1}) != "" {
		t.Error("чистый отчёт не должен получать добивку")
	}
}
