package subagents

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestPoolEnabledConcurrentAccess — гонка между /agents on|off и запусками.
//
// Смысл теста. Переключатель субагентов приходит из UI-потока, а Enabled()
// зовут агентные горутины в начале Spawn. Поле enabled без мьютекса даёт
// честную data race, но обычный go test её не видит: нужен -race. Поэтому
// тест специально гоняет запись и чтение вперемешку и обязан запускаться
// под -race — иначе он ничего не проверяет.
func TestPoolEnabledConcurrentAccess(t *testing.T) {
	p := NewPool(func(context.Context, Spec) (Outcome, error) {
		return Outcome{Full: "отчёт"}, nil
	}, PoolOptions{MaxParallel: 4, MaxDepth: 2, Enabled: true})

	var wg sync.WaitGroup
	stop := time.Now().Add(300 * time.Millisecond)

	// Пишущая сторона: переключатель из UI.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; time.Now().Before(stop); i++ {
			p.SetEnabled(i%2 == 0)
		}
	}()

	// Читающая сторона: агенты проверяют, разрешено ли им работать.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(stop) {
				_ = p.Enabled()
			}
		}()
	}

	// Сторона, которая берёт слот и регистрирует запуск: под -race она
	// поймала бы гонку, даже если Enabled() вызывается не напрямую.
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(stop) {
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				_, _ = p.Spawn(ctx, mkSpec("racy"))
				cancel()
			}
		}()
	}

	wg.Wait()
}

// TestPoolSetEnabledVisible — переключатель действительно влияет на запуск.
func TestPoolSetEnabledVisible(t *testing.T) {
	p := NewPool(func(context.Context, Spec) (Outcome, error) {
		return Outcome{Full: "отчёт"}, nil
	}, PoolOptions{MaxParallel: 2, MaxDepth: 1, Enabled: false})

	if p.Enabled() {
		t.Fatal("пул с Enabled=false должен быть выключен")
	}
	if _, err := p.Spawn(context.Background(), mkSpec("x")); err == nil {
		t.Fatal("выключенный пул должен отказывать в запуске")
	}

	p.SetEnabled(true)
	if !p.Enabled() {
		t.Fatal("после SetEnabled(true) пул должен быть включён")
	}
	if _, err := p.Spawn(context.Background(), mkSpec("x")); err != nil {
		t.Fatalf("включённый пул должен запускать субагента: %v", err)
	}

	p.SetEnabled(false)
	if p.Enabled() {
		t.Fatal("после SetEnabled(false) пул должен быть выключен")
	}
}

// TestWrapAttemptsExact — число запусков равно ровно числу попыток.
//
// Раньше после исчерпания цикла Wrap делал ещё один вызов Runner, и
// Attempts=2 давал три запуска: платная работа делалась дважды, а поле
// Retries показывало 1, и по отчёту нельзя было понять, что было на самом
// деле.
func TestWrapAttemptsExact(t *testing.T) {
	for _, attempts := range []int{1, 2, 3} {
		var calls int
		// Раннер всегда возвращает негодный отчёт — так цикл честно
		// исчерпывает попытки, и мы видим, сколько раз он был вызван.
		r := Wrap(func(context.Context, Spec) (Outcome, error) {
			calls++
			return Outcome{Full: ""}, nil
		}, Resilience{Attempts: attempts, Backoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond})

		_, _ = r(context.Background(), Spec{Name: "a", Type: "explorer", Task: "t"})

		if calls != attempts {
			t.Errorf("Attempts=%d: раннер вызван %d раз, ожидалось %d", attempts, calls, attempts)
		}
	}
}

// Повтор — это попытка минус первая: успех ма первой попытки даёт 0, успех
// после двух неудач даёт 2. Именно тисло шолден главный агент, чтобы понять, сколько
// на самом деле стоил негодный субагент.
// goodReport — заведомо годный отчёт explorer'а: обязательные секции
// контракта на месте, штатки со списком.
func goodReport() string {
	return "## Найдено\n- проверено: 3 файла\n- найдена: 0 проблем\n\n## Вывод\n- Итог: всё в порядке."
}

func TestWrapRetriesCounted(t *testing.T) {
	// Успех с первой попытки: повторов не было.
	calls := 0
	ok := Wrap(func(context.Context, Spec) (Outcome, error) {
		calls++
		return Outcome{Full: goodReport()}, nil
	}, Resilience{Attempts: 3, Backoff: time.Millisecond, MaxBackoff: time.Millisecond})
	out, err := ok(context.Background(), Spec{Name: "a", Type: "explorer", Task: "t"})
	if err != nil {
		t.Fatalf("ожидался успех: %v", err)
	}
	if calls != 1 {
		t.Fatalf("раннер вызван %d раз, ожидался 1", calls)
	}
	if out.Retries != 0 {
		t.Errorf("Retries=%d, ожидался 0 (успех с первой попытки)", out.Retries)
	}

	// Две негодные попытки, третья удачная: повторов было два.
	calls = 0
	late := Wrap(func(context.Context, Spec) (Outcome, error) {
		calls++
		if calls < 3 {
			return Outcome{Full: ""}, nil
		}
		return Outcome{Full: goodReport()}, nil
	}, Resilience{Attempts: 3, Backoff: time.Millisecond, MaxBackoff: time.Millisecond})
	out, err = late(context.Background(), Spec{Name: "b", Type: "explorer", Task: "t"})
	if err != nil {
		t.Fatalf("ожидался успех: %v", err)
	}
	if calls != 3 {
		t.Fatalf("раннер вызван %d раз, ожидался 3", calls)
	}
	if out.Retries != 2 {
		t.Errorf("Retries=%d, ожидалось 2 (две негодные попытки)", out.Retries)
	}
}

// TestWrapStopsOnCancelBeforeAnyRun — отмена не порождает новых запусков.
func TestWrapStopsOnCancelBeforeAnyRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	r := Wrap(func(context.Context, Spec) (Outcome, error) {
		calls++
		return Outcome{Full: ""}, nil
	}, Resilience{Attempts: 5, Backoff: time.Second, MaxBackoff: time.Second})

	if _, err := r(ctx, Spec{Name: "a", Type: "explorer", Task: "t"}); err == nil {
		t.Fatal("после отмены ожидалась ошибка")
	}
	if calls != 0 {
		t.Errorf("после отмены раннер вызван %d раз, ожидалось 0", calls)
	}
}

// TestWrapNonTransientNoRetry — постоянная ошибка не повторяется.
func TestWrapNonTransientNoRetry(t *testing.T) {
	calls := 0
	r := Wrap(func(context.Context, Spec) (Outcome, error) {
		calls++
		return Outcome{}, context.Canceled
	}, Resilience{Attempts: 3, Backoff: time.Millisecond, MaxBackoff: time.Millisecond})

	_, _ = r(context.Background(), Spec{Name: "a", Type: "explorer", Task: "t"})
	if calls != 1 {
		t.Errorf("для невременной ошибки раннер вызван %d раз, ожидался 1", calls)
	}
}

// TestWrapDegradedReportMentionsAttempts — отчёт говорит правду о попытках.
func TestWrapDegradedReportMentionsAttempts(t *testing.T) {
	r := Wrap(func(context.Context, Spec) (Outcome, error) {
		return Outcome{Full: "обрывок"}, nil
	}, Resilience{Attempts: 2, Backoff: time.Millisecond, MaxBackoff: time.Millisecond})

	out, _ := r(context.Background(), Spec{Name: "a", Type: "explorer", Task: "t"})
	if out.Full == "" {
		t.Fatal("ожидался непустой отчёт")
	}
	// Отчёт должен называть ровно 2 попытки: при Attempts=2 именно столько
	// попыток было сделано, и именно это важно читателю.
	if !strings.Contains(out.Full, "2 попытки") {
		t.Errorf("отчёт не упоминает число попыток:\n%s", out.Full)
	}
}
