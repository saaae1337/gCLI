package tui

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuf — буфер, который пишет рендерер и читает тест: без мьютекса
// гонка ловится -race.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// waitFor — дождаться условия с таймаутом: тесты не должны зависать.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("не дождались: %s", what)
}

// TestDocLazyStream — ответ без текста не оставляет пустую карточку и не
// встаёт перед размышлениями.
func TestDocLazyStream(t *testing.T) {
	var d Doc
	d.BeginStream()
	d.ToolStart(ToolInfo{Name: "read_file", Args: "a.go", Kind: "read"})
	d.ToolEnd(ToolInfo{Name: "read_file", Kind: "read", Status: "ok"})
	d.EndStream()
	if len(d.blocks) != 1 || d.blocks[0].kind != blockTool {
		t.Fatalf("ждали одну карточку инструмента, получили %d блоков", len(d.blocks))
	}

	var d2 Doc
	d2.BeginStream()
	d2.Text("привет")
	if len(d2.blocks) != 0 {
		t.Fatalf("до перевода строки блок создаваться не должен")
	}
	d2.Text(", мир\n")
	if len(d2.blocks) != 1 || d2.blocks[0].kind != blockAssistant {
		t.Fatalf("ждали блок ответа, получили %#v", d2.blocks)
	}
	if got := d2.LastAssistant(); got != "привет, мир" {
		t.Fatalf("ответ = %q", got)
	}
	d2.EndStream()
}

// TestDocCarriageReturn — CR перерисовывает строку, а не добавляет новую.
func TestDocCarriageReturn(t *testing.T) {
	var d Doc
	d.Text("скачано 10%\rскачано 55%\rскачано 100%\nготово\n")
	if len(d.blocks) != 1 || len(d.blocks[0].lines) != 2 {
		t.Fatalf("ждали две строки, получили %#v", d.blocks)
	}
	if got := d.blocks[0].lines[0]; got != "скачано 100%" {
		t.Fatalf("последняя перерисовка = %q", got)
	}
	var e Doc
	// CRLF — обычный перевод строки, а не возврат каретки.
	e.Text("раз\r\nдва\r\n")
	if got := strings.Join(e.blocks[0].lines, "|"); got != "раз|два" {
		t.Fatalf("CRLF разобран как %q", got)
	}
}

// TestDocThinkChunks — рассуждения приходят кусками и склеиваются в строки.
func TestDocThinkChunks(t *testing.T) {
	var d Doc
	d.BeginStream()
	d.BeginThink()
	d.ThinkText("Смотрю ")
	d.ThinkText("реализацию.")
	d.ThinkText("\nПотом правлю лимит.\n")
	d.EndThink()
	if len(d.blocks) != 1 || d.blocks[0].kind != blockThink {
		t.Fatalf("ждали один блок мыслей, получили %#v", d.blocks)
	}
	lines := d.blocks[0].lines
	if len(lines) != 2 || lines[0] != "Смотрю реализацию." || lines[1] != "Потом правлю лимит." {
		t.Fatalf("строки мыслей: %#v", lines)
	}
}

// TestDocKeepsANSI — разметка старых панелей доходит до parseStyled, а не
// превращается в мусор «[38;2;…m».
func TestDocKeepsANSI(t *testing.T) {
	const red = "\x1b[31mкрасный\x1b[0m"
	var d Doc
	d.Text(red + "\n")
	if got := d.blocks[0].lines[0]; got != red {
		t.Fatalf("ANSI потерян: %q", got)
	}
	if got := trimControl(red); got != "красный" {
		t.Fatalf("trimControl = %q", got)
	}
}

// TestLayoutWidth — раскладка не выходит за ширину экрана.
func TestLayoutWidth(t *testing.T) {
	var d Doc
	d.AddUser("Почини ретраи в transport.go и прогони тесты, пожалуйста. Проверь лимит и backoff.")
	d.BeginThink()
	d.ThinkText("Смотрю реализацию. Нужно поднять лимит до пяти и добавить паузу.")
	d.EndThink()
	d.ToolStart(ToolInfo{Name: "read_file", Args: "transport.go", Kind: "read", Detail: "240 строк", Status: "ok"})
	d.BeginStream()
	long := strings.Repeat("Очень длинная строка ответа с переносами. ", 8)
	d.Text(long + "\n")
	d.EndStream()
	d.SetMeta("18.4с  ·  41 tok/s  ·  ↑18.4k ↓3.2k")

	for _, w := range []int{40, 60, 80, 100, 140} {
		lines := d.Layout(LayoutOpts{Width: w, Theme: Dark(), Spinner: "⠋"})
		for i, l := range lines {
			if lw := l.Width(); lw > w {
				t.Fatalf("ширина %d: строка %d длиной %d: %q", w, i, lw, l.Text())
			}
		}
	}
}

// TestComposerBasics — ввод, перенос строки и отправка.
func TestComposerBasics(t *testing.T) {
	c := NewComposer()
	c.Insert("привет")
	if c.Empty() {
		t.Fatal("после ввода поле не должно быть пустым")
	}
	c.Insert("\nмир")
	if got := c.Submit(); got != "привет\nмир" {
		t.Fatalf("Submit = %q", got)
	}
	if !c.Empty() {
		t.Fatal("после Submit поле должно очиститься")
	}
	c.Insert("раз")
	c.SetText("")
	if !c.Empty() {
		t.Fatal("SetText(\"\") должен очищать")
	}
}

// TestParseKeys — базовые клавиши терминала.
func TestParseKeys(t *testing.T) {
	r := NewReader(strings.NewReader("a\r\x1b[A\x03"))
	defer r.Close()
	var evs []Event
	deadline := time.After(2 * time.Second)
	for len(evs) < 4 {
		select {
		case ev, ok := <-r.Events():
			if !ok {
				t.Fatalf("поток закрылся, события: %#v", evs)
			}
			evs = append(evs, ev)
		case <-deadline:
			t.Fatalf("не дождались событий: %#v", evs)
		}
	}
	if evs[0].Key != KeyRune || evs[0].R != 'a' {
		t.Fatalf("первое событие: %#v", evs[0])
	}
	if evs[1].Key != KeyEnter {
		t.Fatalf("Enter: %#v", evs[1])
	}
	if evs[2].Key != KeyUp {
		t.Fatalf("стрелка вверх: %#v", evs[2])
	}
	if evs[3].Key != KeyRune || !evs[3].Ctrl || evs[3].R != 'c' {
		t.Fatalf("Ctrl+C: %#v", evs[3])
	}
}

// newTestApp — приложение с поддельным терминалом.
func newTestApp(in io.Reader, out io.Writer) *App {
	return New(Options{
		Version:  "test",
		Model:    "test-model",
		Provider: "test-provider",
		Commands: []Command{{Name: "/help", Desc: "справка"}},
		Color:    true,
		Unicode:  true,
		In:       in,
		Out:      out,
		Raw:      func() (func(), bool) { return func() {}, true },
		Size:     func() (int, int) { return 80, 24 },
	})
}

// TestAppLoop — полный цикл: ввод, ход, ответ, выход.
func TestAppLoop(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	var out syncBuf
	app := newTestApp(pr, &out)

	got := make(chan string, 8)
	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(Handlers{
			Submit: func(text string) {
				got <- text
				if text == "/exit" {
					app.Quit()
					return
				}
				app.Text("ответ на " + text + "\n")
				app.BeginThink()
				app.ThinkText("проверяю лимит и паузу")
				app.EndThink()
				app.SetMetrics(Metrics{Model: "test-model", Context: 100, ContextMax: 1000, Requests: 1})
			},
		})
	}()

	waitFor(t, "запуск", app.Running)
	if _, err := io.WriteString(pw, "привет\r"); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s != "привет" {
			t.Fatalf("Submit получил %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ход не начался")
	}
	waitFor(t, "ответ в ленте", func() bool {
		return strings.Contains(stripANSI(out.String()), "ответ на привет")
	})

	// Ctrl+O не должен блокировать цикл: обработчик мыслей раньше звался
	// под уже взятым мьютексом.
	if _, err := io.WriteString(pw, "\x0f"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(pw, "/exit\r"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run вернул ошибку: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("интерфейс не завершился")
	}
}

// TestAppQueue — запрос во время хода ждёт очереди и появляется в ленте.
func TestAppQueue(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	var out syncBuf
	app := newTestApp(pr, &out)

	texts := make(chan string, 4)
	release := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(Handlers{Submit: func(s string) {
			texts <- s
			switch s {
			case "первый":
				<-release
			case "/exit":
				app.Quit()
			}
		}})
	}()
	waitFor(t, "запуск", app.Running)
	if _, err := io.WriteString(pw, "первый\r"); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-texts:
		if s != "первый" {
			t.Fatalf("первый обработчик получил %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("первый ход не начался")
	}
	// Второй запрос уходит в очередь: ход ещё идёт.
	if _, err := io.WriteString(pw, "второй\r"); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case s := <-texts:
		if s != "второй" {
			t.Fatalf("из очереди пришло %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("очередь не разобралась")
	}
	if _, err := io.WriteString(pw, "/exit\r"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("интерфейс не завершился")
	}
	app.mu.Lock()
	var users []string
	for _, b := range app.doc.blocks {
		if b.kind == blockUser {
			users = append(users, b.lines[0])
		}
	}
	app.mu.Unlock()
	if len(users) != 3 || users[0] != "первый" || users[1] != "второй" || users[2] != "/exit" {
		t.Fatalf("запросы в ленте: %#v", users)
	}
}

// TestAppConfirm — подтверждение приходит окном, а не из stdin.
//
// Русские буквы означают слово, а не позицию клавиши: «д» — да, «н» — нет.
// Перепутать их в подтверждении записи опаснее, чем не понять раскладку.
func TestAppConfirm(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		want Choice
	}{
		{"да", "д", ChoiceYes},
		{"нет", "н", ChoiceNo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testConfirmKey(t, tc.key, tc.want)
		})
	}
}

// testConfirmKey — открыть подтверждение и ответить одной клавишей.
func testConfirmKey(t *testing.T, key string, want Choice) {
	t.Helper()
	pr, pw := io.Pipe()
	defer pw.Close()
	var out syncBuf
	app := newTestApp(pr, &out)

	answer := make(chan Choice, 1)
	asked := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(Handlers{Submit: func(text string) {
			if text == "/exit" {
				app.Quit()
				return
			}
			asked <- struct{}{}
			answer <- app.Confirm(Confirm{
				Title:       "Изменить файл?",
				Path:        "a.go",
				Old:         "one\n",
				New:         "two\n",
				AllowAlways: true,
			})
		}})
	}()
	waitFor(t, "запуск", app.Running)
	if _, err := io.WriteString(pw, "go\r"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-asked:
	case <-time.After(2 * time.Second):
		t.Fatal("подтверждение не открылось")
	}
	// Подтверждение могло открыться чуть позже сигнала, поэтому шлём
	// клавишу, пока не придёт ответ.
	deadline := time.After(2 * time.Second)
confirmLoop:
	for {
		select {
		case ch := <-answer:
			if ch != want {
				t.Fatalf("ответ = %v, ждали %v", ch, want)
			}
			break confirmLoop
		case <-time.After(20 * time.Millisecond):
			if _, err := io.WriteString(pw, key); err != nil {
				t.Fatal(err)
			}
		case <-deadline:
			t.Fatal("подтверждение не закрылось")
		}
	}
	if _, err := io.WriteString(pw, "/exit\r"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("интерфейс не завершился")
	}
}

// TestAppWheelScroll — колесо вверх листает ленту вверх, вниз — ко дну.
func TestAppWheelScroll(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	var out syncBuf
	app := newTestApp(pr, &out)
	errCh := make(chan error, 1)
	go func() { errCh <- app.Run(Handlers{}) }()
	waitFor(t, "запуск", app.Running)
	app.Text(strings.Repeat("строка ленты\n", 80))
	waitFor(t, "лента", func() bool {
		app.mu.Lock()
		defer app.mu.Unlock()
		return len(app.cache) > app.bodyHeight()
	})
	if _, err := io.WriteString(pw, "\x1b[<64;10;10M"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "колесо вверх", func() bool {
		app.mu.Lock()
		defer app.mu.Unlock()
		return app.scroll > 0
	})
	if _, err := io.WriteString(pw, "\x1b[<65;10;10M"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "колесо вниз", func() bool {
		app.mu.Lock()
		defer app.mu.Unlock()
		return app.scroll == 0
	})
	app.Quit()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("интерфейс не завершился")
	}
}

// TestAppCtrlCExit — одно нажатие только подсказывает, второе выходит.
func TestAppCtrlCExit(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	var out syncBuf
	app := newTestApp(pr, &out)
	errCh := make(chan error, 1)
	go func() { errCh <- app.Run(Handlers{}) }()
	waitFor(t, "запуск", app.Running)

	if _, err := io.WriteString(pw, "\x03"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "подсказка", func() bool {
		return strings.Contains(stripANSI(out.String()), "ещё раз ctrl+c")
	})
	if !app.Running() {
		t.Fatal("один Ctrl+C закрыл интерфейс")
	}

	if _, err := io.WriteString(pw, "\x03"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("второй Ctrl+C не закрыл интерфейс")
	}
}

// TestAppCtrlDOnlyIdle — Ctrl+D не закрывает сессию посреди хода.
func TestAppCtrlDOnlyIdle(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	var out syncBuf
	app := newTestApp(pr, &out)

	started := make(chan struct{})
	release := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(Handlers{Submit: func(s string) {
			close(started)
			<-release
		}})
	}()
	waitFor(t, "запуск", app.Running)
	if _, err := io.WriteString(pw, "долгий ход\r"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("ход не начался")
	}
	if _, err := io.WriteString(pw, "\x04"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	if !app.Running() {
		t.Fatal("Ctrl+D закрыл интерфейс во время хода")
	}
	close(release)
	waitFor(t, "ход завершён", func() bool {
		app.mu.Lock()
		defer app.mu.Unlock()
		return !app.busy
	})
	if _, err := io.WriteString(pw, "\x04"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Ctrl+D не закрыл интерфейс на простое")
	}
}

// TestAppAskSecret — ввод секрета маскируется, Esc отменяет вопрос.
func TestAppAskSecret(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	var out syncBuf
	app := newTestApp(pr, &out)
	errCh := make(chan error, 1)
	go func() { errCh <- app.Run(Handlers{}) }()
	waitFor(t, "запуск", app.Running)

	type answer struct {
		s  string
		ok bool
	}
	ch := make(chan answer, 1)
	go func() {
		s, ok := app.Ask(Ask{Question: "Ключ провайдера", Secret: true})
		ch <- answer{s, ok}
	}()
	waitFor(t, "окно вопроса", func() bool {
		app.mu.Lock()
		defer app.mu.Unlock()
		return app.modal != nil && app.modal.kind == modalAsk
	})
	if _, err := io.WriteString(pw, "s3cr3t"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "маска", func() bool {
		return strings.Contains(out.String(), "•")
	})
	if strings.Contains(out.String(), "s3cr3t") {
		t.Fatal("секрет попал в кадр")
	}
	if _, err := io.WriteString(pw, "\r"); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-ch:
		if !a.ok || a.s != "s3cr3t" {
			t.Fatalf("ответ = %q, ok=%v", a.s, a.ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ответ на вопрос не пришёл")
	}

	go func() {
		s, ok := app.Ask(Ask{Question: "Ещё раз?", Secret: true})
		ch <- answer{s, ok}
	}()
	waitFor(t, "второе окно", func() bool {
		app.mu.Lock()
		defer app.mu.Unlock()
		return app.modal != nil
	})
	if _, err := io.WriteString(pw, "\x1b"); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-ch:
		if a.ok {
			t.Fatalf("Esc не отменил вопрос: %q", a.s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Esc не закрыл вопрос")
	}

	app.Quit()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("интерфейс не завершился")
	}
}

// TestAppPalette — Enter доводит команду, второй Enter отправляет.
func TestAppPalette(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	var out syncBuf
	app := newTestApp(pr, &out)
	got := make(chan string, 2)
	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(Handlers{Submit: func(s string) {
			got <- s
			app.Quit()
		}})
	}()
	waitFor(t, "запуск", app.Running)

	if _, err := io.WriteString(pw, "/he"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "палитра", func() bool {
		return strings.Contains(stripANSI(out.String()), "/help")
	})
	if _, err := io.WriteString(pw, "\r"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "команда подставлена", func() bool {
		app.mu.Lock()
		defer app.mu.Unlock()
		return app.comp.Text() == "/help"
	})
	if _, err := io.WriteString(pw, "\r"); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s != "/help" {
			t.Fatalf("Submit получил %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("команда не отправилась")
	}
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("интерфейс не завершился")
	}
}

// TestAppScrollSurvivesTool — новый инструмент не сбрасывает прокрутку.
func TestAppScrollSurvivesTool(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	var out syncBuf
	app := newTestApp(pr, &out)
	errCh := make(chan error, 1)
	go func() { errCh <- app.Run(Handlers{}) }()
	waitFor(t, "запуск", app.Running)
	app.Text(strings.Repeat("строка ленты\n", 80))
	waitFor(t, "лента", func() bool {
		app.mu.Lock()
		defer app.mu.Unlock()
		return len(app.cache) > app.bodyHeight()
	})
	app.mu.Lock()
	app.scrollBy(5)
	app.mu.Unlock()
	app.ToolStart(ToolInfo{Name: "bash", Args: "go test", Kind: "shell"})
	app.mu.Lock()
	got := app.scroll
	app.mu.Unlock()
	if got != 5 {
		t.Fatalf("прокрутка стала %d, ждали 5", got)
	}
	app.Quit()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("интерфейс не завершился")
	}
}

// TestParsePasteCRLF — вставка из Windows не приносит CR в запрос.
func TestParsePasteCRLF(t *testing.T) {
	b := []byte("\x1b[200~первая\r\nвторая\rтретья\x1b[201~")
	ev, n, need := parseKey(b, false)
	if need {
		t.Fatal("вставка разобрана не до конца")
	}
	if n != len(b) {
		t.Fatalf("съедено %d байт из %d", n, len(b))
	}
	if !ev.Paste {
		t.Fatalf("событие не помечено как вставка: %+v", ev)
	}
	want := "первая\nвторая\nтретья"
	if ev.Text != want {
		t.Fatalf("текст вставки = %q, ждали %q", ev.Text, want)
	}
}

// TestParseEsc — одиночный Esc ждёт продолжения, а по таймауту — клавиша.
func TestParseEsc(t *testing.T) {
	if ev, n, need := parseKey([]byte("\x1b"), false); n != 0 || !need || ev.Key != 0 {
		t.Fatalf("без таймаута: ev=%+v n=%d need=%v", ev, n, need)
	}
	ev, n, need := parseKey([]byte("\x1b"), true)
	if need || n != 1 || ev.Key != KeyEsc {
		t.Fatalf("по таймауту: ev=%+v n=%d need=%v", ev, n, need)
	}
}

// TestParseCtrlEnd — модификатор Ctrl у End не теряется: им листается лента.
func TestParseCtrlEnd(t *testing.T) {
	ev, n, need := parseKey([]byte("\x1b[1;5F"), false)
	if need || n != 6 {
		t.Fatalf("Ctrl+End: n=%d need=%v", n, need)
	}
	if ev.Key != KeyEnd || !ev.Ctrl {
		t.Fatalf("Ctrl+End разобран как %+v", ev)
	}
	ev, _, _ = parseKey([]byte("\x1b[F"), false)
	if ev.Key != KeyEnd || ev.Ctrl {
		t.Fatalf("End разобран как %+v", ev)
	}
}
