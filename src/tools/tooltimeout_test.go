package tools

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestToolCtx — приоритеты потолка времени вызова.
//
// Здесь единственная точка, где агент режет зависший вызов инструмента.
// Перепутанный приоритет делает потолок фиктивным: общий лимит бьёт
// индивидуальный, а отсутствие лимитов обязано возвращать nil-кансел —
// иначе вызвавший дернёт nil и упадёт.
func TestToolCtx(t *testing.T) {
	r := New(Env{})
	if r.ToolTimeout() != 0 {
		t.Fatalf("по умолчанию потолка нет, а есть %v", r.ToolTimeout())
	}
	ctx := context.Background()

	// Без лимитов — исходный контекст и nil-кансел: вызов живёт
	// в контексте хода агента, прежнее поведение.
	if c, cf := r.ToolCtx(ctx, &Tool{}); c != ctx || cf != nil {
		t.Fatal("без лимитов ToolCtx обязан вернуть исходный контекст и nil-кансел")
	}

	// Общий лимит.
	r.SetToolTimeout(50 * time.Millisecond)
	if r.ToolTimeout() != 50*time.Millisecond {
		t.Fatalf("общий лимит не встал: %v", r.ToolTimeout())
	}
	// Ноль не затирает заданное: выключение потолка — это не SetToolTimeout(0),
	// а его отсутствие на старте; иначе случайный ноль снял бы страховку.
	r.SetToolTimeout(0)
	if r.ToolTimeout() != 50*time.Millisecond {
		t.Fatalf("SetToolTimeout(0) не должен сбрасывать: %v", r.ToolTimeout())
	}
	c, cf := r.ToolCtx(ctx, &Tool{})
	if c == ctx || cf == nil {
		t.Fatal("с общим лимитом ждут новый контекст и кансел")
	}
	dl, ok := c.Deadline()
	if !ok || time.Until(dl) > time.Second {
		t.Fatalf("общий лимит не применился: %v", dl)
	}
	cf()

	// Индивидуальный лимит приоритетнее общего.
	t2, cf2 := r.ToolCtx(ctx, &Tool{Timeout: time.Hour})
	if cf2 == nil {
		t.Fatal("ждут кансел для индивидуального лимита")
	}
	dl, ok = t2.Deadline()
	if !ok || time.Until(dl) < 59*time.Minute {
		t.Fatalf("индивидуальный лимит часа не приоритетен: %v", dl)
	}
	cf2()

	// nil-инструмент — родительский контекст без ограничений.
	if c, cf := r.ToolCtx(ctx, nil); c != ctx || cf != nil {
		t.Fatal("nil-инструмент обязан вернуть исходный контекст и nil-кансел")
	}
}

// TestToolTimeoutEnv — GCLI_TOOL_TIMEOUT разбирается в New,
// мусорное значение молча игнорируется: потолок — страховка,
// а не повод отказываться стартовать.
func TestToolTimeoutEnv(t *testing.T) {
	t.Setenv("GCLI_TOOL_TIMEOUT", "2s")
	if r := New(Env{}); r.ToolTimeout() != 2*time.Second {
		t.Fatalf("GCLI_TOOL_TIMEOUT=2s не распознан: %v", r.ToolTimeout())
	}
	t.Setenv("GCLI_TOOL_TIMEOUT", "мусор")
	if r := New(Env{}); r.ToolTimeout() != 0 {
		t.Fatalf("мусорное значение обязано игнорироваться: %v", r.ToolTimeout())
	}
}

// TestToolCtxDeadlineCut — обработчик, ждущий контекста, реально
// обрывается по потолку, а не висит дальше.
func TestToolCtxDeadlineCut(t *testing.T) {
	r := New(Env{})
	r.SetToolTimeout(30 * time.Millisecond)
	slow := &Tool{
		Def: ToolDef{Name: "slow"},
		Handler: func(ctx context.Context, _ map[string]any) (Result, error) {
			<-ctx.Done()
			return Result{}, ctx.Err()
		},
	}
	c, cf := r.ToolCtx(context.Background(), slow)
	defer cf()
	start := time.Now()
	_, err := slow.Handler(c, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ждут DeadlineExceeded, получено %v", err)
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("медленный вызов не был прерван вовремя: %v", el)
	}
}
