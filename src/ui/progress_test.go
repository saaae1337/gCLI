package ui

import (
	"strings"
	"sync"
	"testing"
)

// TestProgressPrintsCounterAndLabel — прогресс показывает счётчик «3/7» и
// подпись цели. Без счётчика пользователь не отличит «идёт пятый файл» от
// «зависло», а по одной подписи без числа непонятно, сколько ещё ждать.
func TestProgressPrintsCounterAndLabel(t *testing.T) {
	u, buf := testUI()
	u.Progress(ProgressUpdate{Title: "multi_read", Label: "src/ui/ui.go", Done: 3, Total: 7, Ok: true})
	out := buf.String()

	for _, want := range []string{"multi_read", "3/7", "src/ui/ui.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("в прогрессе нет %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("ожидалась ровно одна строка прогресса, получено:\n%q", out)
	}
}

// TestProgressShowsTargetError — ошибка отдельной цели видна сразу, а не
// только в финальной сводке. Модель про ошибку узнает из текста пачки, но
// пользователь смотрит на экран: молча проглоченный сбой читается как «всё
// прошло», и он начнёт доверять неверному отчёту.
func TestProgressShowsTargetError(t *testing.T) {
	u, buf := testUI()
	u.Progress(ProgressUpdate{
		Title: "multi_bash", Label: "go test ./tools/", Done: 2, Total: 3,
		Ok: false, Err: "ненулевой код выхода: 1",
	})
	out := buf.String()
	// Полная строка ошибки не влезет в 60 колонок вместе с командой и
	// счётчиком, поэтому проверяем её смысловую часть: обрезка сокращает
	// хвост, но не начало.
	if !strings.Contains(out, "ненулевой код") {
		t.Errorf("текст ошибки цели не показан:\n%s", out)
	}
	if !strings.Contains(out, "go test") {
		t.Errorf("не показано, какая именно команда упала:\n%s", out)
	}
}

// TestProgressFinalIsSummaryNotCounter — финальное событие печатается
// сводкой. Счётчик «7/7» после итоговой фразы выглядел бы как начало
// новой операции, а следующая карточка инструмента только усилила бы
// путаницу.
func TestProgressFinalIsSummaryNotCounter(t *testing.T) {
	u, buf := testUI()
	u.Progress(ProgressUpdate{Title: "multi_read", Label: "7 прочитано, 0 с ошибкой", Done: 7, Total: 7, Final: true})
	out := buf.String()
	if strings.Contains(out, "7/7") {
		t.Errorf("финальная сводка не должна дублировать счётчик:\n%s", out)
	}
	if !strings.Contains(out, "7 прочитано") {
		t.Errorf("сводка потеряна:\n%s", out)
	}
}

// TestProgressFinalWithoutLabel — пустая сводка не печатает пустую строку.
func TestProgressFinalWithoutLabel(t *testing.T) {
	u, buf := testUI()
	u.Progress(ProgressUpdate{Title: "multi_read", Final: true})
	if !strings.Contains(buf.String(), "готово") {
		t.Errorf("ожидалась подпись «готово»:\n%q", buf.String())
	}
}

// TestProgressRespectsWidth — длинная подпись цели обрезается по ширине
// терминала. Без обрезки длинный путь или вывод команды уезжает за правый
// край и ломает весь рельс ниже.
func TestProgressRespectsWidth(t *testing.T) {
	buf := &strings.Builder{}
	u := New(Options{Color: false, Unicode: true, Width: 30, Out: buf})
	u.Progress(ProgressUpdate{
		Title: "multi_read",
		Label: strings.Repeat("очень-длинный-путь-к-файлу-", 10),
		Done:  1, Total: 3, Ok: true,
	})
	line := strings.TrimRight(buf.String(), "\n")
	if n := visibleWidth(line); n > 30 {
		t.Errorf("строка шире терминала: %d колонок вместо 30\n%q", n, line)
	}
	// Метка обрезки: «…» (одна колонка) или «...». Раньше это было ровно
	// «...», теперь обрезка идёт по колонкам, и «…» даёт на два символа
	// больше полезного текста — поэтому проверяем сам факт метки, а не её вид.
	if !strings.HasSuffix(line, "...") && !strings.HasSuffix(line, "…") {
		t.Errorf("длинная подпись не помечена обрезкой:\n%q", line)
	}
}

// TestProgressQuietPrintsNothing — в тихом режиме (например -p) прогресс не
// должен попадать в машинный вывод: он ломает парсинг результата.
func TestProgressQuietPrintsNothing(t *testing.T) {
	u, buf := testUI()
	u.SetQuiet(true)
	u.Progress(ProgressUpdate{Title: "multi_read", Label: "a.go", Done: 1, Total: 2, Ok: true})
	if buf.String() != "" {
		t.Errorf("в тихом режиме прогресс напечатан:\n%q", buf.String())
	}
}

// TestProgressConcurrentIsSafe — события приходят из горутий инструментов.
// Без мьютекса строки перемешиваются и портятся цветовые последовательности;
// -race здесь ловит именно гонку, а проверка ниже — битый вывод.
func TestProgressConcurrentIsSafe(t *testing.T) {
	u, buf := testUI()
	var wg sync.WaitGroup
	const n = 30
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u.Progress(ProgressUpdate{
				Title: "multi_read", Label: "файл.go", Done: i + 1, Total: n, Ok: true,
			})
		}(i)
	}
	wg.Wait()

	out := buf.String()
	if got := strings.Count(out, "\n"); got != n {
		t.Errorf("строк прогресса %d, ожидалось %d", got, n)
	}
	// Каждая строка должна быть целой: escape-последовательности не
	// разрываются посередине и метка видна в каждой.
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if !strings.Contains(l, "файл.go") {
			t.Errorf("строка прогресса потеряна или разорвана:\n%q", l)
		}
	}
}

// TestProgressMixedWithAnswer — прогресс не должен съедать строку ответа:
// печать гасит ожидание, но сама остаётся отдельной строкой.
func TestProgressMixedWithAnswer(t *testing.T) {
	u, buf := testUI()
	u.Progress(ProgressUpdate{Title: "multi_read", Label: "a.go", Done: 1, Total: 1, Ok: true})
	u.Println("ответ модели")
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("ожидались две строки (прогресс и ответ), получено %d:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[1], "ответ модели") {
		t.Errorf("ответ искажён:\n%s", out)
	}
}
