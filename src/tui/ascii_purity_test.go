package tui

// Тест чистоты оформления: интерфейс печатает только ASCII и кириллицу.
//
// Правило проекта: ни один глиф за пределами U+007F и кириллицы не
// попадает на экран. Псевдографика (│ ─ ╭), стрелки (↑ →), типографские
// кавычки («») и тире (—) не рисуются терминалами Windows 10/11 без
// смены шрифта или кодовой страницы. Этот тест ловит случайный символ
// в любом месте кадра: шапка, лента, окна, подсказки, статус.

import "testing"

// asciiOrCyrillic — руна допустима в кадре.
func asciiOrCyrillic(r rune) bool {
	if r < 0x7F { // ASCII, включая пробел и перевод строки
		return true
	}
	return r >= 0x0400 && r <= 0x04FF // кириллица, включая ё
}

func assertFrameASCII(t *testing.T, name string, s *Screen) {
	t.Helper()
	for y := 0; y < s.H; y++ {
		for x := 0; x < s.W; x++ {
			r := s.Cells[y*s.W+x].R
			if r == 0 {
				continue
			}
			if !asciiOrCyrillic(r) {
				t.Errorf("кадр %s: клетка (%d,%d) — недопустимая руна %q (U+%04X)", name, x, y, r, r)
				return
			}
		}
	}
}

// TestFramesASCIIOnly — все отладочные сценарии превью на трёх ширинах
// не содержат ни одного символа вне ASCII и кириллицы.
func TestFramesASCIIOnly(t *testing.T) {
	scenarios := []struct {
		name string
		fn   func(w, h int) *App
	}{
		{"welcome", func(w, h int) *App { return previewApp(w, h) }},
		{"dialog", func(w, h int) *App {
			a := previewApp(w, h)
			fillDialogue(a)
			return a
		}},
		{"confirm", func(w, h int) *App {
			a := previewApp(w, h)
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
			}
			a.modal.diff = diffWindow(lineDiff(
				"func retry(n int) error {\n\tif n > 3 {\n\t\treturn errLimit\n\t}\n\treturn nil\n}",
				"func retry(n int) error {\n\tif n > 5 {\n\t\treturn errLimit\n\t}\n\treturn backoff(n)\n}"), 24)
			return a
		}},
		{"palette", func(w, h int) *App {
			a := previewApp(w, h)
			fillDialogue(a)
			a.comp.SetText("/")
			a.palSel = 1
			return a
		}},
		{"ask", func(w, h int) *App {
			a := previewApp(w, h)
			fillDialogue(a)
			a.modal = &modal{
				kind:   modalAsk,
				title:  "Вопрос инструмента",
				detail: "Какой файл открыть для правки? Можно выбрать вариант или написать свой путь.",
				opts: []modalOpt{
					{Label: "src/providers/transport.go"},
					{Label: "src/providers/keys.go"},
				},
				input: NewComposer(),
				ask:   make(chan askResult, 1),
				reply: make(chan Choice, 1),
			}
			a.modal.input.SetText("src/prov")
			return a
		}},
		{"busy", func(w, h int) *App {
			a := previewApp(w, h)
			fillDialogue(a)
			a.busy = true
			a.activity = "bash go test ./..."
			a.tick = 1
			return a
		}},
		{"scroll", func(w, h int) *App {
			a := previewApp(w, h)
			fillDialogue(a)
			a.scroll = 12
			return a
		}},
	}
	for _, sc := range scenarios {
		for _, w := range []int{60, 80, 110, 140} {
			h := 40
			if w == 60 {
				h = 24
			}
			a := sc.fn(w, h)
			assertFrameASCII(t, sc.name, a.buildLocked())
		}
	}
}

// TestDocTextASCIIOnly — лента переживает любой текст из инструментов:
// в кадре остаются только маркеры оформления и сам текст. Управляющие
// и декоративные символы из ответа модели вычищаются на входе (см.
// cleanControl), а глифы оформления — ASCII по константам темы.
func TestDocTextASCIIOnly(t *testing.T) {
	a := previewApp(80, 40)
	a.doc.AddUser("запрос с «типографикой», — тире и … многоточием")
	a.doc.BeginThink()
	a.doc.ThinkText("мысли с → стрелкой и • буллетом")
	a.doc.EndThink()
	a.doc.ToolStart(ToolInfo{ID: "t1", Name: "bash", Args: "echo · привет", Kind: "exec"})
	a.doc.ToolEnd(ToolInfo{ID: "t1", Name: "bash", Kind: "exec", Status: "ok", Elapsed: 50000000, Detail: "+1 −1 · 12 строк"})
	a.doc.BeginStream()
	a.doc.Text("ответ с ↑ стрелками и ✓ галочками\n")
	a.doc.EndStream()
	a.doc.SetMeta("18.4s | 41 tok/s")
	a.busy = false
	assertFrameASCII(t, "doc-text", a.buildLocked())
}
