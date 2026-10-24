package agent

import (
	"context"
	"strings"
	"testing"

	"gcli/core"
	"gcli/providers"
	"gcli/tools"
)

// ---------- сжатие истории ----------

// plainSession — сессия без ReplaceMessages: запасной путь replaceMessages.
type plainSession struct {
	msgs []core.Message
}

func (s *plainSession) Messages() []core.Message  { return s.msgs }
func (s *plainSession) AddMessage(m core.Message) { s.msgs = append(s.msgs, m) }
func (s *plainSession) AddUsage(core.Usage)       {}
func (s *plainSession) Turns() int                { return 0 }
func (s *plainSession) SetTodos([]tools.TodoItem) {}
func (s *plainSession) Todos() []tools.TodoItem   { return nil }

// compactAgent — агент со скриптом провайдера для одного запроса.
func compactAgent(t *testing.T, sess *plainSession, summary string) *Agent {
	t.Helper()
	sp := &scriptProvider{steps: [][]string{textStep(summary)}}
	return New(Deps{
		Providers: providers.NewClient(),
		Provider:  sp.provider(t),
		Session:   sess,
		Registry:  tools.New(tools.Env{}),
		Model:     "тест",
	}, ".")
}

// TestCompactKeepsSummaryInFallback — сжатие существует ради того, чтобы
// выжимка сохранилась. Запасной путь схлопывал её через core.OneLine в
// одну строку: от многоабзацной выжимки оставалось первое предложение, и
// следующий ход начинал с нуля — то есть платил за запрос к модели и не
// получал ничего.
func TestCompactKeepsSummaryInFallback(t *testing.T) {
	const summary = "Задача: починить падения.\n\nЧто сделано:\n- разобран цикл агента\n- добавлен тест\n\n" +
		"Что осталось:\n- починить сортировку\n- обновить CHANGELOG"
	sess := &plainSession{msgs: []core.Message{
		{Role: core.RoleUser, Content: "почини падения"},
		{Role: core.RoleAssistant, Content: "смотрю"},
	}}
	a := compactAgent(t, sess, summary)

	if !a.Compact(context.Background(), false) {
		t.Fatal("Compact вернул false: сжатие не состоялось")
	}
	var text strings.Builder
	for _, m := range sess.Messages() {
		text.WriteString(m.Content)
		text.WriteByte('\n')
	}
	got := text.String()
	if !strings.Contains(got, "починить сортировку") {
		t.Errorf("конец выжимки потерян — OneLine съел всё после первого абзаца:\n%s", got)
	}
	if !strings.Contains(got, "добавлен тест") {
		t.Errorf("середина выжимки потеряна:\n%s", got)
	}
	if !strings.Contains(got, "История сжата") {
		t.Errorf("нет отметки о сжатии:\n%s", got)
	}
}

// TestCompactUsesReplaceWhenAvailable — если сессия умеет заменять
// историю, ею и надо пользоваться: иначе история растёт вместе с
// выжимкой, и сжатие не освобождает контекст.
func TestCompactUsesReplaceWhenAvailable(t *testing.T) {
	summary := "Выжимка."
	sp := &scriptProvider{steps: [][]string{textStep(summary)}}
	rs := &replaceSession{}
	a := New(Deps{
		Providers: providers.NewClient(),
		Provider:  sp.provider(t),
		Session:   rs,
		Registry:  tools.New(tools.Env{}),
		Model:     "тест",
	}, ".")
	rs.msgs = []core.Message{
		{Role: core.RoleUser, Content: "было много"},
		{Role: core.RoleAssistant, Content: "и ещё"},
	}
	if !a.Compact(context.Background(), false) {
		t.Fatal("Compact вернул false")
	}
	if !rs.replaced {
		t.Error("ReplaceMessages не вызван: история не укорочена, контекст не освобождён")
	}
	if len(rs.msgs) != 1 {
		t.Errorf("после сжатия сообщений %d, ждали 1", len(rs.msgs))
	}
}

// TestCompactTooShortHistory — сжимать нечего, и лишний запрос к модели
// не отправляется.
func TestCompactTooShortHistory(t *testing.T) {
	sess := &plainSession{msgs: []core.Message{{Role: core.RoleUser, Content: "одно"}}}
	a := New(Deps{
		Session:  sess,
		Registry: tools.New(tools.Env{}),
	}, ".")
	if a.Compact(context.Background(), false) {
		t.Error("Compact сработал на одном сообщении")
	}
}

// replaceSession — сессия, умеющая заменять историю.
type replaceSession struct {
	msgs     []core.Message
	replaced bool
}

func (s *replaceSession) Messages() []core.Message  { return s.msgs }
func (s *replaceSession) AddMessage(m core.Message) { s.msgs = append(s.msgs, m) }
func (s *replaceSession) AddUsage(core.Usage)       {}
func (s *replaceSession) Turns() int                { return 0 }
func (s *replaceSession) SetTodos([]tools.TodoItem) {}
func (s *replaceSession) Todos() []tools.TodoItem   { return nil }
func (s *replaceSession) ReplaceMessages(m []core.Message) {
	s.replaced = true
	s.msgs = m
}
