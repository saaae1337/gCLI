package subagents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---------- IsTransient ----------

func TestIsTransient(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"поток закрыт", errors.New("stream error: stream ID 89; STREAM_CLOSED; received from peer"), true},
		{"eof", errors.New("unexpected EOF"), true},
		{"перегрузка", errors.New("503 Service Unavailable"), true},
		{"лимит запросов", errors.New("429 Too Many Requests"), true},
		{"таймаут контекста", context.DeadlineExceeded, true},
		{"обрыв соединения", errors.New("read: connection reset by peer"), true},
		{"отмена", context.Canceled, false},
		{"нет прав", errors.New("достигнута максимальная глубина вложенности"), false},
		{"пул выключен", errors.New("субагенты отключены"), false},
		{"плохая задача", errors.New("укажи task"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsTransient(c.err); got != c.want {
				t.Errorf("IsTransient(%v) = %v, ожидалось %v", c.err, got, c.want)
			}
		})
	}
}

// ---------- AssessReport ----------

func TestAssessReport(t *testing.T) {
	cases := []struct {
		name string
		full string
		want Quality
	}{
		{"пусто", "", ReportEmpty},
		{"пробелы", "   \n  ", ReportEmpty},
		{"нормальный отчёт", "1. Сделано: добавил инструмент в tools/self.go\n2. Проверено: go test проходит.", ReportOK},
		{"мысль вслух", "сейчас ещё посмотрю", ReportNarrative},
		{"мысль с кодовым словом", "Let me get the remaining pieces…", ReportNarrative},
		{"обрыв на полуслове", "- Проверил файлы\n- Дальше посмотрю", ReportTruncated},
		{"обрыв многоточием", "- Нашёл два места в коде...", ReportTruncated},
		{"честная неудача", "Субагент не смог сформулировать отчёт: закончил работу без результата.", ReportOK},
		{"ошибка сети", "Ошибка: stream error: STREAM_CLOSED", ReportOK},
		{"длинный отчёт с выводом", strings.Repeat("Строка вывода по проекту.\n", 30) + "Итог: готово.", ReportOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AssessReport(c.full); got != c.want {
				t.Errorf("AssessReport(%q) = %v, ожидалось %v", trunc(c.full), got, c.want)
			}
		})
	}
}

func trunc(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

// ---------- Wrap: ретрай при сбое ----------

func TestWrapRetriesOnTransient(t *testing.T) {
	var calls int32
	r := Wrap(func(_ context.Context, spec Spec) (Outcome, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return Outcome{}, errors.New("stream error: STREAM_CLOSED; received from peer")
		}
		// Модель должна получить объяснение, почему это повтор.
		if spec.RetryHint == "" {
			t.Errorf("повтор запущен без RetryHint: модель повторит ту же неудачную тактику")
		}
		return Outcome{Full: "1. Готово.\n2. Проверено: тесты проходят."}, nil
	}, Resilience{Attempts: 2, Backoff: time.Millisecond, MaxBackoff: time.Millisecond})

	out, err := r(context.Background(), Spec{Type: TypeExplorer, Task: "изучить код"})
	if err != nil {
		t.Fatalf("ожидался успех после повтора, получено: %v", err)
	}
	if out.Full == "" {
		t.Error("отчёт пуст")
	}
	if out.Retries != 1 {
		t.Errorf("Retries = %d, ожидалось 1", out.Retries)
	}
	if calls != 2 {
		t.Errorf("вызовов runner: %d, ожидалось 2", calls)
	}
}

func TestWrapNoRetryOnFatal(t *testing.T) {
	var calls int32
	r := Wrap(func(_ context.Context, _ Spec) (Outcome, error) {
		atomic.AddInt32(&calls, 1)
		return Outcome{}, errors.New("достигнута максимальная глубина вложенности")
	}, Resilience{Attempts: 3, Backoff: time.Millisecond, MaxBackoff: time.Millisecond})

	if _, err := r(context.Background(), Spec{Type: TypeCustom}); err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if calls != 1 {
		t.Errorf("вызовов runner: %d, ожидалось 1 — фатальный сбой не повторяется", calls)
	}
}

// ---------- Wrap: добивка негодного отчёта ----------

func TestWrapRetriesOnNarrativeReport(t *testing.T) {
	var calls int32
	r := Wrap(func(_ context.Context, _ Spec) (Outcome, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			// Худший случай: цикл оборвался на мысли вслух, но err == nil.
			return Outcome{Full: "сейчас ещё посмотрю"}, nil
		}
		return Outcome{Full: "## Итог\n- Проверен tools/registry.go:42\n- Добавлен инструмент."}, nil
	}, Resilience{Attempts: 2, Backoff: time.Millisecond, MaxBackoff: time.Millisecond})

	out, err := r(context.Background(), Spec{Type: TypeExplorer, Task: "изучить код"})
	if err != nil {
		t.Fatalf("ожидался успех, получено: %v", err)
	}
	if calls != 2 {
		t.Errorf("вызовов runner: %d, ожидалось 2 — обрывок отчёта должен вызывать повтор", calls)
	}
	if !strings.Contains(out.Full, "Добавлен инструмент") {
		t.Errorf("в отчёте нет результата повторной попытки: %q", trunc(out.Full))
	}
}

// Главный сценарий: отчёт так и не удалось получить — агент не должен
// получить молчание, он должен понять, что произошло.
func TestWrapAlwaysExplainsFailure(t *testing.T) {
	r := Wrap(func(_ context.Context, _ Spec) (Outcome, error) {
		return Outcome{Full: "подождите"}, nil
	}, Resilience{Attempts: 2, Backoff: time.Millisecond, MaxBackoff: time.Millisecond})

	out, err := r(context.Background(), Spec{Type: TypeReviewer, Task: "ревью"})
	_ = err
	if !strings.Contains(out.Full, "Отчёт неполный") {
		t.Errorf("ожидалось объяснение сбоя, получено: %q", trunc(out.Full))
	}
	if !strings.Contains(out.Full, "не полагаться") {
		t.Errorf("нет подсказки главному агенту, что делать: %q", trunc(out.Full))
	}
}

func TestWrapDisabled(t *testing.T) {
	var calls int32
	orig := func(_ context.Context, _ Spec) (Outcome, error) {
		atomic.AddInt32(&calls, 1)
		return Outcome{Full: "сейчас посмотрю"}, nil
	}
	if got := Wrap(orig, Resilience{Attempts: 1}); got == nil {
		t.Fatal("Wrap вернул nil при Attempts=1")
	}
	out, _ := orig(context.Background(), Spec{})
	if out.Full == "" || calls != 1 {
		t.Error("базовый runner должен работать как есть")
	}
}

func TestWrapStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int32
	r := Wrap(func(_ context.Context, _ Spec) (Outcome, error) {
		atomic.AddInt32(&calls, 1)
		cancel()
		return Outcome{}, errors.New("stream error: STREAM_CLOSED")
	}, Resilience{Attempts: 3, Backoff: 10 * time.Millisecond, MaxBackoff: 10 * time.Millisecond})

	_, _ = r(ctx, Spec{Type: TypeExplorer})
	if n := atomic.LoadInt32(&calls); n > 2 {
		t.Errorf("вызовов после отмены: %d — отмена не должна повторяться", n)
	}
}

func TestRetryHintMentionsPreviousFailure(t *testing.T) {
	h := RetryHint(errors.New("STREAM_CLOSED"), ReportNarrative)
	for _, want := range []string{"STREAM_CLOSED", "мыслью вслух", "Не начинай исследование заново"} {
		if !strings.Contains(h, want) {
			t.Errorf("в RetryHint нет %q:\n%s", want, h)
		}
	}
}

func TestPoolSpawnPropagatesRetries(t *testing.T) {
	var n int32
	p := NewPool(func(_ context.Context, _ Spec) (Outcome, error) {
		if atomic.AddInt32(&n, 1) == 1 {
			return Outcome{}, errors.New("stream error: STREAM_CLOSED")
		}
		return Outcome{Full: "- готово", Retries: 1}, nil
	}, PoolOptions{Enabled: true, MaxParallel: 2, MaxDepth: 1})
	p.runner = Wrap(p.runner, Resilience{Attempts: 2, Backoff: time.Millisecond, MaxBackoff: time.Millisecond})

	out, err := p.Spawn(context.Background(), Spec{Type: TypeExplorer, Task: "задача"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if out.Retries != 1 {
		t.Errorf("Retries = %d, ожидалось 1", out.Retries)
	}
	if d := p.Details("explorer-1"); !strings.Contains(d, "готов") {
		t.Errorf("отчёт не записан в пул:\n%s", d)
	}}

func TestQualityTextAllCases(t *testing.T) {
	for _, q := range []Quality{ReportEmpty, ReportNarrative, ReportTruncated} {
		if QualityText(q) == "" {
			t.Errorf("QualityText(%v) пуст", q)
		}
	}
	if QualityText(ReportOK) != "" {
		t.Error("для хорошего отчёта объяснение не нужно")
	}
	_ = fmt.Sprint()
}
