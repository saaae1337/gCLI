package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// infosRepo — репозиторий во временном домашнем каталоге.
func infosRepo(t *testing.T) *Repo {
	t.Helper()
	t.Setenv("GCLI_HOME", t.TempDir())
	return &Repo{Store: NewStore(), Cfg: DefaultConfig()}
}

// TestListSessionInfosMatchesFullLoad — шапка не врёт против полной загрузки.
//
// Быстрый список имеет смысл, только пока показывает те же тему, время
// и число сообщений, что и полная загрузка. Расхождение здесь — молча
// врущий список сессий, и заметят его пользователи раньше теста.
func TestListSessionInfosMatchesFullLoad(t *testing.T) {
	r := infosRepo(t)
	s := &Session{
		ID:       "abc123",
		Title:    "тема сессии",
		Created:  time.Now().Add(-time.Hour),
		Provider: "zai", Model: "glm-4.6", Turns: 3,
		Usage: Usage{PromptTokens: 100, CompletionTokens: 50},
		Stats: Stats{Requests: 2, Tools: 7},
		CWD:   "/tmp/proj",
	}
	for i := 0; i < 4; i++ {
		s.Messages = append(s.Messages, Message{Role: "user", Content: "привет"})
	}
	if err := r.SaveSession(s); err != nil {
		t.Fatal(err)
	}
	infos := r.ListSessionInfos()
	if len(infos) != 1 {
		t.Fatalf("ждут одну сессию, получено %d", len(infos))
	}
	got := infos[0]
	full, err := r.LoadSession("abc123")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != full.ID || got.Title != full.Title ||
		!got.Updated.Equal(full.Updated) || !got.Created.Equal(full.Created) ||
		got.Provider != full.Provider || got.Model != full.Model ||
		got.Turns != full.Turns || got.Usage != full.Usage || got.Stats != full.Stats ||
		got.CWD != full.CWD {
		t.Fatalf("шапка разошлась с полной загрузкой:\n got  %+v\n full %+v", got, *full)
	}
	if got.Messages != len(full.Messages) {
		t.Fatalf("число сообщений: %d, ждут %d", got.Messages, len(full.Messages))
	}
}

// TestListSessionInfosEmpty — нет каталога и нет файлов: nil и пусто,
// без паники. Вызывается на старте до первой сессии.
func TestListSessionInfosEmpty(t *testing.T) {
	r := infosRepo(t)
	if got := r.ListSessionInfos(); len(got) != 0 {
		t.Fatalf("на пустом хранилище ждут пустой список, получено %d", len(got))
	}
}

// TestScanSessionHeaderSkipsBigTail — большой хвост неизвестных полей
// и длинные сообщения не мешают шапке и не всплывают в неё.
//
// Смысл потокового разбора — не держать переписку в памяти: если бы
// пропуск ломался на длинных строках или вложенности, быстрым списком
// можно было бы вернуть полный Unmarshal.
func TestScanSessionHeaderSkipsBigTail(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"id":"x1","title":"т","created":"2026-10-25T10:00:00Z",`)
	b.WriteString(`"updated":"2026-10-25T11:00:00Z","provider":"zai","model":"glm-4.6",`)
	// Неизвестные шапке поля — пропуск целиком, с вложенностями.
	b.WriteString(`"perms":{"bash":true,"bash_exact":{"a b":true,"c":false}},"economy":{"saved_calls":1},`)
	b.WriteString(`"checkpoints":[{"path":"x","label":"l","files":[{"p":"q"}]}],`)
	b.WriteString(`"messages":[`)
	for i := 0; i < 50; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		// Внутри сообщения — строка со скобками и вложенный объект:
		// счётчик обязан не сбиться ни на строках, ни на глубине.
		b.WriteString(`{"role":"user","content":"скобки }{[] и ` + strings.Repeat("х", 500) + `","tool_calls":[{"name":"n","args":"{}"}]}`)
	}
	b.WriteString(`],"turns":9,"usage":{"prompt_tokens":10,"completion_tokens":5},`)
	b.WriteString(`"stats":{"requests":1,"tokens_in":10},"cwd":"/w"}`)

	si, err := scanSessionHeader(json.NewDecoder(strings.NewReader(b.String())))
	if err != nil {
		t.Fatal(err)
	}
	if si.ID != "x1" || si.Title != "т" || si.Provider != "zai" || si.Model != "glm-4.6" {
		t.Fatalf("шапка неверна: %+v", si)
	}
	if si.Turns != 9 || si.Usage.PromptTokens != 10 || si.Usage.CompletionTokens != 5 ||
		si.Stats.Requests != 1 || si.CWD != "/w" {
		t.Fatalf("хвостовые поля не разобрались: %+v", si)
	}
	if si.Messages != 50 {
		t.Fatalf("сообщений: %d, ждут 50", si.Messages)
	}
	if si.Created.IsZero() || si.Updated.IsZero() {
		t.Fatal("времена обязаны разобраться")
	}
}

// TestListSessionInfosNullMessages — пустая сессия пишет
// «messages":null» (Marshal nil-слайса), и это не повод выкидывать её
// из списка: полный Unmarshal null терпел, быстрый обязан так же.
func TestListSessionInfosNullMessages(t *testing.T) {
	r := infosRepo(t)
	if err := r.SaveSession(&Session{ID: "empty1", Title: "пустая"}); err != nil {
		t.Fatal(err)
	}
	infos := r.ListSessionInfos()
	if len(infos) != 1 {
		t.Fatalf("пустая сессия пропала из списка: %d", len(infos))
	}
	if infos[0].Messages != 0 {
		t.Fatalf("сообщений: %d, ждут 0", infos[0].Messages)
	}
}

// TestCountJSONArrayShapes — счётчик не путается на формах элементов.
func TestCountJSONArrayShapes(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{`[]`, 0},
		{`[1,2,3]`, 3},
		{`["a]","{","]"]`, 3},    // скаляры-строки со скобками
		{`[{"a":[1,2]}]`, 1},     // один объект с вложенным массивом
		{`[{"a":1},{"b":2}]`, 2}, // объекты подряд
		{`[[1],[2,3]]`, 2},       // массивы подряд
	}
	for _, c := range cases {
		got, err := countJSONArray(json.NewDecoder(strings.NewReader(c.in)))
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("%s: получено %d, ждут %d", c.in, got, c.want)
		}
	}
}

// TestSkipJSONValue — после пропуска значения поток продолжается корректно.
func TestSkipJSONValue(t *testing.T) {
	dec := json.NewDecoder(strings.NewReader(
		`{"k":{"a":{"b":[1,{"c":"}{["}]},"d":"}"},"after":1}`))
	if _, err := dec.Token(); err != nil { // '{' корня
		t.Fatal(err)
	}
	tok, err := dec.Token()
	if err != nil || tok != "k" {
		t.Fatalf("ждут ключ k: %v %v", tok, err)
	}
	if err := skipJSONValue(dec); err != nil {
		t.Fatal(err)
	}
	tok, err = dec.Token()
	if err != nil || tok != "after" {
		t.Fatalf("после пропуска ждут ключ after: %v %v", tok, err)
	}
}
