package agent

import (
	"strings"
	"testing"

	"gcli/core"
)

// tc — вспомогательный конструктор вызова инструмента.
func tc(name, args string) core.ToolCall {
	return core.ToolCall{ID: name + "_" + args, Name: name, Args: args}
}

// tr — вспомогательный конструктор результата инструмента.
func tr(name, text string) toolResult {
	return toolResult{tc: tc(name, ""), text: text}
}

func TestLoopDetectorIgnoresSingleCall(t *testing.T) {
	d := NewLoopDetector()
	for i := 0; i < 2; i++ {
		if w := d.Record([]core.ToolCall{tc("read_file", `{"path":"a.go"}`)}, nil); w != "" {
			t.Fatalf("ложное срабатывание на %d вызове: %s", i+1, w)
		}
	}
}

func TestLoopDetectorWarnsOnRepeatedCall(t *testing.T) {
	d := NewLoopDetector()
	calls := []core.ToolCall{tc("grep", `{"pattern":"foo"}`)}
	var warn string
	for i := 0; i < loopWarnAfter; i++ {
		warn = d.Record(calls, nil)
	}
	if warn == "" {
		t.Fatal("ожидалось предупреждение о повторе")
	}
	for _, want := range []string{"Стоп", "grep", "вернёт тот же результат"} {
		if !strings.Contains(warn, want) {
			t.Errorf("в предупреждении нет %q:\n%s", want, warn)
		}
	}
	// Повторное предупреждение о том же самом не нужно — оно бы съедало контекст.
	if again := d.Record(calls, nil); again != "" {
		t.Errorf("предупреждение продублировано:\n%s", again)
	}
}

func TestLoopDetectorIgnoresFormattingDifferences(t *testing.T) {
	d := NewLoopDetector()
	// Тот же вызов, отличается только форматирование JSON.
	d.Record([]core.ToolCall{tc("read_file", `{"path":"a.go","offset":1}`)}, nil)
	d.Record([]core.ToolCall{tc("read_file", "{\n  \"path\": \"a.go\",\n  \"offset\": 1\n}")}, nil)
	w := d.Record([]core.ToolCall{tc("read_file", `{"path": "a.go", "offset": 1}`)}, nil)
	if w == "" {
		t.Error("разное форматирование тех же аргументов должно считаться петлёй")
	}
}

func TestLoopDetectorDifferentArgsAreNotLoop(t *testing.T) {
	d := NewLoopDetector()
	for i := 0; i < 5; i++ {
		w := d.Record([]core.ToolCall{tc("read_file", `{"path":"file`+string(rune('a'+i))+`.go"}`)}, nil)
		if w != "" {
			t.Fatalf("разные файлы — не петля: %s", w)
		}
	}
}

func TestLoopDetectorWarnsOnRepeatedError(t *testing.T) {
	d := NewLoopDetector()
	var warn string
	for i := 0; i < loopSameErrAfter; i++ {
		warn = d.Record(
			[]core.ToolCall{tc("edit_file", `{"path":"x.go","old_string":"a","new_string":"`+string(rune('a'+i))+`"}`)},
			[]toolResult{tr("edit_file", "Ошибка: old_string не найден в файле")},
		)
	}
	if warn == "" {
		t.Fatal("ожидалось предупреждение об одинаковой ошибке")
	}
	if !strings.Contains(warn, "old_string") {
		t.Errorf("в предупреждении нет самой ошибки:\n%s", warn)
	}
}

func TestLoopDetectorIgnoresRepeatedSuccess(t *testing.T) {
	d := NewLoopDetector()
	// Сборка дважды подряд — нормально, детектор молчит.
	for i := 0; i < 4; i++ {
		w := d.Record(
			[]core.ToolCall{tc("bash", `{"command":"go build ./...`+string(rune('0'+i))+`"}`)},
			[]toolResult{tr("bash", "сборка успешно")},
		)
		if w != "" {
			t.Fatalf("успешный вызов не должен считаться петлёй: %s", w)
		}
	}
}

func TestLoopDetectorWindowSlides(t *testing.T) {
	d := NewLoopDetector()
	// Много разных вызовов вытесняют старый из окна.
	calls := make([]core.ToolCall, 0, loopLookback+4)
	for i := 0; i < loopLookback+4; i++ {
		calls = append(calls, tc("read_file", `{"path":"`+string(rune('a'+i))+`"}`))
	}
	var last string
	for _, c := range calls {
		last = d.Record([]core.ToolCall{c}, nil)
	}
	if last != "" {
		t.Errorf("разные вызовы дали ложное срабатывание: %s", last)
	}
	// Первый вызов давно вытеснен — повторять его уже не считается петлёй.
	for i := 0; i < 2; i++ {
		if w := d.Record([]core.ToolCall{tc("read_file", `{"path":"a.go"}`)}, nil); w != "" {
			t.Errorf("вытесненный из окна вызов не должен давать петлю: %s", w)
		}
	}
}

func TestLoopDetectorSingleIterationErrorsAreNotStreak(t *testing.T) {
	d := NewLoopDetector()
	// Ошибка, успех, ошибка, успех — не полоса.
	res := [][]toolResult{
		{tr("edit_file", "Ошибка: не найдено")},
		{tr("edit_file", "готово")},
		{tr("edit_file", "Ошибка: не найдено")},
		{tr("edit_file", "готово")},
		{tr("edit_file", "Ошибка: не найдено")},
	}
	var warn string
	for i, r := range res {
		warn = d.Record([]core.ToolCall{tc("edit_file", `{"attempt":"`+string(rune('a'+i))+`"}`)}, r)
	}
	if warn != "" {
		t.Errorf("чередующиеся ошибки — не полоса:\n%s", warn)
	}
}

func TestCallKeyStableAcrossWhitespace(t *testing.T) {
	a := callKey("bash", `{"command":"go test"}`)
	b := callKey("bash", "{ \"command\" : \"go test\" }")
	if a != b {
		t.Errorf("ключи различаются: %q vs %q", a, b)
	}
	c := callKey("bash", `{"command":"go vet"}`)
	if a == c {
		t.Error("разные команды должны давать разные ключи")
	}
	d := callKey("grep", `{"command":"go test"}`)
	if a == d {
		t.Error("разные инструменты должны давать разные ключи")
	}
}

func TestErrKeyOnlyForProblems(t *testing.T) {
	if errKey("Ошибка: не найдено") == "" {
		t.Error("ошибка должна распознаваться")
	}
	if errKey("Предупреждение: файл пуст") == "" {
		t.Error("предупреждение должно распознаваться")
	}
	if errKey("Файл прочитан, 200 строк") != "" {
		t.Error("успешный вывод не должен попадать в ключ ошибки")
	}
	if errKey("") != "" {
		t.Error("пустой результат не должен попадать в ключ ошибки")
	}
}

func TestLoopDetectorConcurrent(t *testing.T) {
	d := NewLoopDetector()
	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func(g int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 50; i++ {
				d.Record(
					[]core.ToolCall{tc("read_file", `{"n":"`+string(rune(g*50+i))+`"}`)},
					[]toolResult{tr("read_file", "ок")},
				)
			}
		}(g)
	}
	for g := 0; g < 8; g++ {
		<-done
	}
	if _, dropped := d.Stats(); dropped < 0 {
		t.Error("счётчик вытесненных не может быть отрицательным")
	}
}
