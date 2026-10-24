package tui

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPreviewFrames — отладочный рендер кадров в HTML.
//
// Запускается только с GCLI_TUI_PREVIEW=1: в обычном прогоне ничего не
// пишет. Нужен, чтобы смотреть интерфейс глазами на разных ширинах, не
// поднимая raw-терминал.
func TestPreviewFrames(t *testing.T) {
	if os.Getenv("GCLI_TUI_PREVIEW") == "" {
		t.Skip("GCLI_TUI_PREVIEW не задан")
	}
	dir := filepath.Join(os.TempDir(), "gcli-tui-preview")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, w := range []int{80, 110, 140} {
		// 1. Пустая сессия.
		a := previewApp(w, 40)
		writeFrame(t, filepath.Join(dir, fmt.Sprintf("welcome-%d.html", w)), a.buildLocked())
		writeText(t, filepath.Join(dir, fmt.Sprintf("welcome-%d.txt", w)), a.buildLocked())
		if w == 80 {
			// 1б. Низкое окно: подсказки важнее маскота.
			a = previewApp(w, 24)
			writeFrame(t, filepath.Join(dir, "welcome-low-80.html"), a.buildLocked())
			writeText(t, filepath.Join(dir, "welcome-low-80.txt"), a.buildLocked())
		}

		// 2. Диалог: запрос, размышления, карточки, ответ, метрики.
		a = previewApp(w, 44)
		fillDialogue(a)
		writeFrame(t, filepath.Join(dir, fmt.Sprintf("dialog-%d.html", w)), a.buildLocked())
		writeText(t, filepath.Join(dir, fmt.Sprintf("dialog-%d.txt", w)), a.buildLocked())

		// 3. Подтверждение записи с диффом.
		a = previewApp(w, 44)
		fillDialogue(a)
		a.modal = &modal{
			kind:  modalConfirm,
			title: "Изменить файл?",
			path:  "src/providers/transport.go",
			note:  "правка обработки лимита",
			opts: []modalOpt{
				{Key: 'y', Label: "разрешить", Choice: ChoiceYes},
				{Key: 'a', Label: "всегда", Choice: ChoiceAlways},
				{Key: 'n', Label: "отклонить", Choice: ChoiceNo},
			},
			sel: 0,
		}
		a.modal.diff = diffWindow(lineDiff("func retry(n int) error {\n\tif n > 3 {\n\t\treturn errLimit\n\t}\n\treturn nil\n}", "func retry(n int) error {\n\tif n > 5 {\n\t\treturn errLimit\n\t}\n\treturn backoff(n)\n}"), 24)
		writeFrame(t, filepath.Join(dir, fmt.Sprintf("confirm-%d.html", w)), a.buildLocked())
		writeText(t, filepath.Join(dir, fmt.Sprintf("confirm-%d.txt", w)), a.buildLocked())

		// 4. Палитра команд.
		a = previewApp(w, 40)
		fillDialogue(a)
		a.comp.SetText("/")
		a.palSel = 1
		writeFrame(t, filepath.Join(dir, fmt.Sprintf("palette-%d.html", w)), a.buildLocked())
		writeText(t, filepath.Join(dir, fmt.Sprintf("palette-%d.txt", w)), a.buildLocked())

		// 5. Вопрос инструмента: варианты и поле ответа.
		a = previewApp(w, 44)
		fillDialogue(a)
		a.modal = &modal{
			kind:   modalAsk,
			title:  "Вопрос инструмента",
			detail: "Какой файл открыть для правки? Можно выбрать вариант или написать свой путь.",
			opts: []modalOpt{
				{Label: "src/providers/transport.go"},
				{Label: "src/providers/keys.go"},
			},
			sel:   0,
			input: NewComposer(),
		}
		a.modal.input.Insert("src/prov")
		writeFrame(t, filepath.Join(dir, fmt.Sprintf("ask-%d.html", w)), a.buildLocked())
		writeText(t, filepath.Join(dir, fmt.Sprintf("ask-%d.txt", w)), a.buildLocked())

		// 6. Лог с возвратом каретки и бегущей карточкой.
		a = previewApp(w, 32)
		a.doc.AddUser("Собери проект и покажи прогресс")
		a.doc.ToolStart(ToolInfo{Name: "bash", Args: "go build ./...", Kind: "exec", Detail: "сборка", Status: "running"})
		a.doc.Text("шаг 1/3: зависимости\n")
		a.doc.Text("сборка 10%\rсборка 45%\rсборка 80%\rсборка 100%\n")
		a.doc.Text("готово\n")
		a.busy = true
		a.tick = 5
		writeFrame(t, filepath.Join(dir, fmt.Sprintf("log-%d.html", w)), a.buildLocked())
		writeText(t, filepath.Join(dir, fmt.Sprintf("log-%d.txt", w)), a.buildLocked())

		if w == 80 {
			// 7. Низкий терминал: кнопки подтверждения обязаны остаться.
			a = previewApp(w, 20)
			fillDialogue(a)
			a.modal = &modal{
				kind:  modalConfirm,
				title: "Изменить файл?",
				path:  "src/providers/transport.go",
				note:  "правка обработки лимита",
				opts: []modalOpt{
					{Key: 'y', Label: "разрешить", Choice: ChoiceYes},
					{Key: 'n', Label: "отклонить", Choice: ChoiceNo},
				},
			}
			a.modal.diff = diffWindow(lineDiff("func retry(n int) error {\n\tif n > 3 {\n\t\treturn errLimit\n\t}\n\treturn nil\n}", "func retry(n int) error {\n\tif n > 5 {\n\t\treturn errLimit\n\t}\n\treturn backoff(n)\n}"), 24)
			writeFrame(t, filepath.Join(dir, "confirm-low-80.html"), a.buildLocked())
			writeText(t, filepath.Join(dir, "confirm-low-80.txt"), a.buildLocked())

			// 8. Секрет: ключ виден только точками.
			a = previewApp(w, 30)
			a.modal = &modal{
				kind:   modalAsk,
				title:  "Секрет",
				detail: "Вставьте ключ провайдера. Он не появится ни в ленте, ни на экране.",
				secret: true,
				input:  NewComposer(),
			}
			a.modal.input.Insert("sk-live-0123456789abcdef")
			writeFrame(t, filepath.Join(dir, "secret-80.html"), a.buildLocked())
			writeText(t, filepath.Join(dir, "secret-80.txt"), a.buildLocked())
		}
	}
	t.Logf("кадры: %s", dir)
}

func previewApp(w, h int) *App {
	a := New(Options{
		Version:  "0.7.0.0",
		Model:    "glm-4.6",
		Provider: "Z.ai",
		WorkDir:  `C:\gcli\source\gcli`,
		Session:  "ses_7f3a",
		Commands: []Command{
			{Name: "/model", Desc: "сменить модель"},
			{Name: "/usage", Desc: "расход токенов"},
			{Name: "/clear", Desc: "очистить экран"},
		},
		Color:   true,
		Unicode: true,
	})
	a.started = true
	a.w, a.h = w, h
	a.met = Metrics{
		Model:      "glm-4.6",
		Provider:   "Z.ai",
		Context:    48200,
		ContextMax: 200000,
		Prompt:     18400,
		Completion: 3200,
		Requests:   7,
		Rate:       41.4,
		Last:       18*time.Second + 400*time.Millisecond,
		Session:    12*time.Minute + 5*time.Second,
		Cost:       0.42,
		CostKnown:  true,
	}
	return a
}

func fillDialogue(a *App) {
	a.doc.AddUser("Почини ретраи в transport.go: лимит должен быть 5, и добавь backoff. Потом прогони тесты.")
	a.doc.BeginThink()
	a.doc.ThinkText("Сначала посмотрю текущую реализацию и вызовы. Потом поменяю число и добавлю паузу между попытками.")
	a.doc.EndThink()
	a.doc.ToolStart(ToolInfo{Name: "read_file", Args: "transport.go", Kind: "read", Detail: "240 строк", Status: "running"})
	a.doc.ToolEnd(ToolInfo{Name: "read_file", Kind: "read", Detail: "240 строк", Status: "ok", Elapsed: 180 * time.Millisecond, Lines: 240})
	a.doc.ToolStart(ToolInfo{Name: "edit_file", Args: "transport.go", Kind: "write", Detail: "+3 −2", Status: "running"})
	a.doc.ToolEnd(ToolInfo{Name: "edit_file", Kind: "write", Detail: "+3 −2", Status: "ok", Elapsed: 90 * time.Millisecond, Lines: 3})
	a.doc.ToolStart(ToolInfo{Name: "bash", Args: "go test ./providers/...", Kind: "exec", Detail: "12 строк", Status: "running"})
	a.doc.ToolEnd(ToolInfo{Name: "bash", Kind: "exec", Detail: "12 строк", Status: "ok", Elapsed: 2*time.Second + 100*time.Millisecond, Lines: 12})
	a.doc.BeginStream()
	a.doc.Text("Готово: лимит поднят до пяти попыток, между ними — нарастающая пауза.\n\n")
	a.doc.Text("Тесты провайдеров проходят, сборка чистая.\n")
	a.doc.EndStream()
	a.doc.SetMeta("18.4с  ·  41 tok/s  ·  ↑18.4k ↓3.2k")
	a.busy = false
	a.tick = 3
}

// writeFrame — кадр в HTML: одна клетка — один span.
func writeFrame(t *testing.T, path string, s *Screen) {
	t.Helper()
	var b strings.Builder
	b.WriteString("<!doctype html><meta charset=\"utf-8\"><style>")
	b.WriteString("body{margin:0;background:#0E0D0B;padding:24px}")
	b.WriteString("pre{font-family:'Cascadia Mono','JetBrains Mono','Consolas','DejaVu Sans Mono',monospace;")
	b.WriteString("font-size:15px;line-height:1.18;letter-spacing:0;margin:0;white-space:pre}")
	b.WriteString("</style><pre>")
	for y := 0; y < s.H; y++ {
		for x := 0; x < s.W; x++ {
			c := s.Cells[y*s.W+x]
			if c.Cont {
				continue
			}
			r := c.R
			if r == 0 {
				r = ' '
			}
			bg := c.S.BG
			fg := c.S.FG
			if !bg.On {
				bg = s.Bg
			}
			if !fg.On {
				fg = s.Bg
			}
			fmt.Fprintf(&b, `<span style="color:#%02x%02x%02x;background:#%02x%02x%02x`,
				fg.R, fg.G, fg.B, bg.R, bg.G, bg.B)
			if c.S.Bold {
				b.WriteString(";font-weight:700")
			}
			if c.S.Dim {
				b.WriteString(";opacity:.62")
			}
			if c.S.Italic {
				b.WriteString(";font-style:italic")
			}
			if c.S.Underline {
				b.WriteString(";text-decoration:underline")
			}
			b.WriteString(`">`)
			ch := string(r)
			if ch == " " {
				ch = "&nbsp;"
			} else {
				ch = html.EscapeString(ch)
			}
			b.WriteString(ch)
			b.WriteString("</span>")
		}
		b.WriteString("\n")
	}
	b.WriteString("</pre>")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeText — тот же кадр простым текстом (быстро посмотреть в консоли).
func writeText(t *testing.T, path string, s *Screen) {
	t.Helper()
	var b strings.Builder
	for y := 0; y < s.H; y++ {
		for x := 0; x < s.W; x++ {
			c := s.Cells[y*s.W+x]
			if c.Cont {
				continue
			}
			if c.R == 0 {
				b.WriteRune(' ')
			} else {
				b.WriteRune(c.R)
			}
		}
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
