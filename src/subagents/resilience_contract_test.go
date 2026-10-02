package subagents

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---------- Wrap × контракт секций × заземление ----------
//
// Тесты ниже проверяют не отдельные функции, а связку «слой прочности →
// контракт → заземление». Именно на стыке она и ломалась: до появления
// контрактов Wrap считал отчёт хорошим по одной лишь форме, а заземление
// подставлялось в текст отчёта и на повтор не влияло.

// fastResilience — ретрай без реальных пауз.
func fastResilience(attempts int) Resilience {
	return Resilience{Attempts: attempts, Backoff: time.Millisecond, MaxBackoff: time.Millisecond}
}

// goodExplorer — эталонный годный отчёт explorer'а: секции на месте, ссылки
// подтверждены журналом.
func goodExplorer() string {
	return "## Найдено\n- Цикл хода в subagents/pool.go:120\n## Вывод\n- Всё в порядке."
}

// groundedExplorer — журнал доказательств под эталонный отчёт: оба файла
// прочитаны целиком, поэтому любая ссылка на них подтверждается.
func groundedExplorer(t *testing.T) *Grounding {
	t.Helper()
	g, dir := mkGrounding(t)
	writeFileOnDisk(t, dir, "subagents/pool.go", 200)
	writeFileOnDisk(t, dir, "subagents/resilient.go", 400)
	g.Observe("read_file", `{"paths":["subagents/pool.go","subagents/resilient.go"]}`,
		readFull("subagents/pool.go", 200)+"\n"+readFull("subagents/resilient.go", 400), true)
	return g
}

// TestWrapRetriesOnMissingSections — отчёт читаемый и правдивый, но без
// обязательных секций: повтор всё равно нужен, потому что главный агент по
// такому тексту не понимает, что делалось и что осталось.
func TestWrapRetriesOnMissingSections(t *testing.T) {
	g := groundedExplorer(t)
	// Отчёт должен быть многострочным и структурным: AssessReport ловит
	// одиночную фразу раньше, чем дойдёт дело до контракта, и повтор тогда
	// объясняет совсем другое («мысль вслух» вместо «нет секций»).
	nosec := "Разобрался с пулом субагентов.\n" +
		"- Цикл запуска в subagents/pool.go:120\n" +
		"- Слой прочности в subagents/resilient.go:271\n" +
		"Итог: всё работает, менять ничего не нужно."
	if q := AssessReport(nosec); q != ReportOK {
		t.Fatalf("фикстура дефектна не по секциям, а по форме: %v", q)
	}
	if ch := CheckSections(nosec, TypeExplorer); ch.OK() {
		t.Fatalf("фикстура уже удовлетворяет контракту: %+v", ch)
	}
	// Ссылки подтверждены: дефект только в форме, и вердикт обязан быть
	// именно ReportNoSections, а не ReportUnverified.
	if audit := g.Audit(nosec); !audit.Trustworthy {
		t.Fatalf("ссылки фикстуры не подтверждены: %+v", audit.Problems())
	}

	var calls int32
	var secondHint string
	r := Wrap(func(_ context.Context, spec Spec) (Outcome, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return Outcome{Full: nosec, Audit: g.Audit(nosec)}, nil
		}
		secondHint = spec.RetryHint
		return Outcome{Full: goodExplorer(), Audit: g.Audit(goodExplorer())}, nil
	}, fastResilience(2))

	out, err := r(context.Background(), Spec{Type: TypeExplorer, Task: "изучить пул"})
	if err != nil {
		t.Fatalf("ожидался успех после повтора, получено: %v", err)
	}
	if calls != 2 {
		t.Fatalf("вызовов runner: %d, ожидалось 2 — отсутствие секций должно вызывать повтор", calls)
	}
	if !strings.Contains(secondHint, "обязательных секций") {
		t.Errorf("подсказка не объяснила, чего не хватило:\n%s", secondHint)
	}
	// Адресная добавка не нужна: нечего открывать, всё подтверждено.
	if strings.Contains(secondHint, "проблемные места") {
		t.Errorf("подсказка требует открыть места при чистом заземлении:\n%s", secondHint)
	}
	if !strings.Contains(out.Full, "## Вывод") {
		t.Errorf("вернулся негодный отчёт первой попытки: %q", trunc(out.Full))
	}
}

// TestWrapRetriesOnPhantomFile — субагент сослался на несуществующий файл.
// Это не оговорка, а выдумка, и повтор обязателен: вторая попытка получает
// адресную подсказку с именем фантома.
func TestWrapRetriesOnPhantomFile(t *testing.T) {
	g := groundedExplorer(t)
	var calls int32
	var secondHint string
	bad := "## Найдено\n- Логика в utils/ghost.go:15\n## Вывод\n- Всё ясно."
	r := Wrap(func(_ context.Context, spec Spec) (Outcome, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return Outcome{Full: bad, Audit: g.Audit(bad)}, nil
		}
		secondHint = spec.RetryHint
		return Outcome{Full: goodExplorer(), Audit: g.Audit(goodExplorer())}, nil
	}, fastResilience(2))

	out, err := r(context.Background(), Spec{Type: TypeExplorer, Task: "изучить пул"})
	if err != nil {
		t.Fatalf("ожидался успех после повтора, получено: %v", err)
	}
	if calls != 2 {
		t.Fatalf("вызовов runner: %d, ожидалось 2 — фантом должен вызывать повтор", calls)
	}
	if !strings.Contains(secondHint, "utils/ghost.go") {
		t.Errorf("в подсказке нет имени фантома:\n%s", secondHint)
	}
	if !strings.Contains(out.Full, "## Найдено") || strings.Contains(out.Full, "ghost") {
		t.Errorf("вернулся отчёт первой попытки: %q", trunc(out.Full))
	}
}

// readRange — результат ЧАСТИЧНОГО чтения конкретного файла: показан
// диапазон строк, до EOF файл не дочитан. readPart из grounding_test.go
// годится для multi_read (хвост один на пачку), но здесь нужен точный
// диапазон поимённо — иначе ссылка на строки 8–9 окажется подтверждённой
// тем же readPart, что показал 1–5.
func readRange(path string, from, shown int) string {
	return "Файл: " + path + "\n\n" +
		"[показано строк: " + itoa(shown) + " начиная с " + itoa(from) +
		"; далее offset=" + itoa(from+shown) + "]"
}

// TestWrapRetryHintIsAddressed — адресный список в подсказке: модель должна
// видеть конкретные места, а не слова «старайся лучше».
//
// Три непроверенных места одного отчёта: две ссылки за пределом прочитанного
// диапазона в СУЩЕСТВУЮЩИХ файлах (файлы субагент видел, строки — нет) и одна
// ссылка на несуществующий файл. Все три обязаны попасть в подсказку: это
// разные виды брака, и лечатся они по-разному.
func TestWrapRetryHintIsAddressed(t *testing.T) {
	g, dir := mkGrounding(t)
	writeFileOnDisk(t, dir, "a.go", 30)
	writeFileOnDisk(t, dir, "b.go", 30)
	g.Observe("read_file", `{"path":"a.go"}`, readRange("a.go", 1, 5), true)
	g.Observe("read_file", `{"path":"b.go"}`, readRange("b.go", 1, 5), true)

	var secondHint string
	bad := "## Найдено\n- a.go:8 и b.go:7 и missing/нет.go:2\n## Вывод\n- Итог."
	// Фикстура обязана быть дефектной: если аудит окажется чистым, повтора
	// не будет и тест проверит совсем не то.
	if audit := g.Audit(bad); len(audit.Problems()) != 3 {
		t.Fatalf("фикстура не дефектна: проблемных мест %d, ожидалось 3: %+v",
			len(audit.Problems()), audit.Problems())
	}
	r := Wrap(func(_ context.Context, spec Spec) (Outcome, error) {
		if spec.RetryHint != "" {
			secondHint = spec.RetryHint
			return Outcome{Full: goodExplorer(), Audit: g.Audit(goodExplorer())}, nil
		}
		return Outcome{Full: bad, Audit: g.Audit(bad)}, nil
	}, fastResilience(2))

	if _, err := r(context.Background(), Spec{Type: TypeExplorer, Task: "изучить"}); err != nil {
		t.Fatalf("ожидался успех, получено: %v", err)
	}
	if secondHint == "" {
		t.Fatal("вторая попытка прошла без RetryHint")
	}
	for _, want := range []string{"a.go:8", "b.go:7", "missing/нет.go"} {
		if !strings.Contains(secondHint, want) {
			t.Errorf("в подсказке нет %q:\n%s", want, secondHint)
		}
	}
}

// TestWrapNoRetryOnSingleUnconfirmedLink — одна неподтверждённая ссылка среди
// нескольких подтверждённых: это обычная оговорка, а не дефект отчёта.
//
// Повтор здесь стоил бы полного перезапуска задачи и почти наверняка вернул бы
// тот же результат: дефект тут в единичном факте, а не в отчёте целиком.
func TestWrapNoRetryOnSingleUnconfirmedLink(t *testing.T) {
	g, dir := mkGrounding(t)
	writeFileOnDisk(t, dir, "big.go", 500)
	g.Observe("read_file", `{"path":"big.go"}`, readPart(1, 200), true)

	var calls int32
	// Одна ссылка за пределами прочитанного диапазона, остальные — внутри.
	full := "## Найдено\n" +
		"- big.go:10 — в норме\n- big.go:50 — в норме\n- big.go:400 — не открывалось\n" +
		"## Вывод\n- Проблема найдена."
	r := Wrap(func(_ context.Context, _ Spec) (Outcome, error) {
		atomic.AddInt32(&calls, 1)
		return Outcome{Full: full, Audit: g.Audit(full)}, nil
	}, fastResilience(3))

	out, _ := r(context.Background(), Spec{Type: TypeExplorer, Task: "изучить"})
	if calls != 1 {
		t.Fatalf("вызовов runner: %d, ожидалась 1 — оговорка не должна вызывать повтор", calls)
	}
	if out.Retries != 0 {
		t.Errorf("Retries = %d, ожидалось 0", out.Retries)
	}
	// Оговорка при этом обязана быть видна. Проверка делится на два слоя:
	// вердикт о повторе (сделал Wrap) и блок в тексте отчёта (сделал
	// RunSubagent, который зовёт Grounding.Report). Здесь проверяем только
	// первый: блока в ответе раннера нет, и требовать его нельзя.
	a := g.Audit(full)
	if len(a.Unsupported) != 1 || a.Unsupported[0].Where != "big.go:400" {
		t.Errorf("неподтверждённая ссылка не выделена: %+v", a.Unsupported)
	}
	if a.Trustworthy {
		t.Error("отчёт с непрочитанной строкой 400 не должен быть trustworthy")
	}
	if !strings.Contains(g.Report(a), "big.go:400") {
		t.Errorf("блок проверки не назвал проблемное место:\n%s", g.Report(a))
	}
}

// TestWrapNoRetryWhenNoFilesRead — субагент не открыл ни одного файла: повтор
// тут не починка отчёта, а новое исследование с нуля, и вердикт должен
// оставить решение главному агенту.
func TestWrapNoRetryWhenNoFilesRead(t *testing.T) {
	var calls int32
	full := "## Найдено\n- Всё очень сложно\n## Вывод\n- Сложно."
	r := Wrap(func(_ context.Context, _ Spec) (Outcome, error) {
		atomic.AddInt32(&calls, 1)
		return Outcome{Full: full, Audit: NewGrounding(t.TempDir()).Audit(full)}, nil
	}, fastResilience(3))

	out, _ := r(context.Background(), Spec{Type: TypeExplorer, Task: "изучить"})
	if calls != 1 {
		t.Fatalf("вызовов runner: %d, ожидалась 1 — без прочитанных файлов повтор бессмыслен", calls)
	}
	if out.Retries != 0 {
		t.Errorf("Retries = %d, ожидалось 0", out.Retries)
	}
}

// TestJudgeReportChecksGroundingLast — порядок проверок: нельзя «договориться»
// с валидатором, выбрав удобную половину. Пока форма и контракт в порядке,
// вердикт обязан дойти до доказательств.
func TestJudgeReportChecksGroundingLast(t *testing.T) {
	spec := Spec{Type: TypeExplorer}
	// Ни формы, ни секций нет — вердикт о форме, а не о доказательствах.
	if got := JudgeReport(spec, Outcome{Full: "сейчас посмотрю"}); got != ReportNarrative {
		t.Errorf("мысль вслух: вердикт %v, ожидался ReportNarrative", got)
	}
	// Форма в порядке, секций нет, аудит идеален — вердикт о секциях.
	clean := NewGrounding(t.TempDir()).Audit(goodExplorer())
	if got := JudgeReport(spec, Outcome{Full: "- просто текст", Audit: clean}); got != ReportNoSections {
		t.Errorf("нет секций: вердикт %v, ожидался ReportNoSections", got)
	}
	// Всё на месте, кроме доказательств — вердикт о заземлении.
	bad := "## Найдено\n- missing/нет.go:2\n## Вывод\n- Итог."
	g := groundedExplorer(t)
	if got := JudgeReport(spec, Outcome{Full: bad, Audit: g.Audit(bad)}); got != ReportUnverified {
		t.Errorf("фантом: вердикт %v, ожидался ReportUnverified", got)
	}
	// Эталон — всё хорошо.
	if got := JudgeReport(spec, Outcome{Full: goodExplorer(), Audit: g.Audit(goodExplorer())}); got != ReportOK {
		t.Errorf("годный отчёт: вердикт %v, ожидался ReportOK", got)
	}
}

// TestNoContractForFreeTypes — у general и custom нет предписанного формата,
// и требовать секции значило бы штрафовать нормальный свободный отчёт.
//
// Проверяется именно контракт: вердикт на такой текст для general/custom
// равняется форме AssessReport, а не ReportNoSections.
func TestNoContractForFreeTypes(t *testing.T) {
	for _, ty := range []Type{TypeGeneral, TypeCustom} {
		if HasContract(ty) {
			t.Errorf("%s: контракт секций не должен требоваться", ty)
		}
		if ch := CheckSections("Просто абзац с ответом, без заголовков.", ty); !ch.OK() {
			t.Errorf("%s: свободный отчёт зачтён как нарушение контракта: %+v", ty, ch.Missing)
		}
	}
	// Многострочный структурный текст без единого заголовка: AssessReport
	// считает его нормальным отчётом, и единственное, что может его
	// забраковать, — контракт секций. А контракта у general нет.
	free := "Разобрался в задаче.\n- Нашёл цикл в pool.go\n- Всё работает\nИтог: можно идти дальше."
	if q := AssessReport(free); q != ReportOK {
		t.Fatalf("тестовый отчёт негоден по форме: %v", q)
	}
	if got := JudgeReport(Spec{Type: TypeGeneral}, Outcome{Full: free}); got != ReportOK {
		t.Errorf("свободный отчёт: вердикт %v, ожидался ReportOK", got)
	}
	if got := JudgeReport(Spec{Type: TypeCustom}, Outcome{Full: "сейчас гляну"}); got != ReportNarrative {
		t.Errorf("мысль вслух: вердикт %v, ожидался ReportNarrative", got)
	}
}

// TestGroundHintOrderStableAfterCap — обрезка до 12 мест происходит ПОСЛЕ
// сортировки, иначе две попытки подряд чинят разные дефекты: список мест
// обязан быть одинаковым от запуска к запуску.
func TestGroundHintOrderStableAfterCap(t *testing.T) {
	var rep GroundingReport
	for i := 1; i <= 30; i++ {
		rep.Unsupported = append(rep.Unsupported, Claim{Where: "f" + itoa(i) + ".go:1"})
	}
	rep.Checked, rep.FilesRead = 30, 1

	h := GroundHint(&rep)
	if h == "" {
		t.Fatal("GroundHint пуст при 30 неподтверждённых ссылках")
	}
	// Список обрезан ровно до 12 мест: длинная подсказка съедает контекст,
	// который модели нужнее, чем хвост из двенадцатого десятого места.
	if n := strings.Count(h, ".go:1\n"); n != 12 {
		t.Errorf("в подсказке %d мест, ожидалось 12 (обрезано после сортировки):\n%s", n, h)
	}
	// Первая строка списка после сортировки обязана быть лексикографически
	// минимальной: без сортировки обрезка оставила бы случайный хвост.
	var first string
	for _, l := range strings.Split(h, "\n") {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "- f") && strings.HasSuffix(t, ".go:1") {
			first = strings.TrimPrefix(t, "- ")
			break
		}
	}
	if first != "f1.go:1" {
		t.Errorf("первым в списке идёт %q, ожидался f1.go:1 — обрезка до сортировки", first)
	}
	if GroundHint(&rep) != h {
		t.Error("GroundHint не детерминирован: два вызова дали разный текст")
	}
	if GroundHint(nil) != "" {
		t.Error("GroundHint(nil) должен быть пустым")
	}
}
