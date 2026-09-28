package ui

import (
	"regexp"
	"strings"
)

// Регулярки markdown.
var (
	reH        = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	reTask     = regexp.MustCompile(`^(\s*)[-*]\s+\[([ xX])\]\s+(.*)$`)
	reBullet   = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	reOrdered  = regexp.MustCompile(`^(\s*)(\d+)[.)]\s+(.*)$`)
	reQuote    = regexp.MustCompile(`^\s*>\s?(.*)$`)
	reHRule    = regexp.MustCompile(`^\s*(-\s*-\s*-|\*\s*\*\s*\*|_\s*_\s*_)[-*_\s]*$`)
	reTableSep = regexp.MustCompile(`^\s*\|?[\s:|-]+\|[\s:|-]*$`)

	// Inline-разметка. Порядок применения важен: сначала код (в нём ничего не трогаем),
	// затем ссылки, жирный, курсив, зачёркивание.
	reCode      = regexp.MustCompile("`+([^`]+)`+")
	reLink      = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	reBareURL   = regexp.MustCompile(`(^|[\s(])(https?://[^\s<>()\[\]"]+)`)
	reBoldStar  = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reBoldUnder = regexp.MustCompile(`__([^_]+)__`)
	reStrike    = regexp.MustCompile(`~~([^~]+)~~`)
	reItalic    = regexp.MustCompile(`(^|[^*\w])\*([^*\n]+)\*`)
)

// streamFence — состояние блока кода для RenderLine.
type fenceKey struct{}

// Stream — потоковый рендерер markdown для стриминга ответа модели.
type Stream struct {
	u     *UI
	pend  string
	state *streamState
}

type streamState struct {
	fence bool
	lang  string
}

// NewStream — создать потоковый рендерер.
func (u *UI) NewStream() *Stream {
	return &Stream{u: u, state: &streamState{}}
}

// Reset — сбросить состояние (начало нового ответа).
func (s *Stream) Reset() {
	s.state.fence = false
	s.state.lang = ""
	s.pend = ""
}

// FenceOpen — внутри ли сейчас блока кода.
func (s *Stream) FenceOpen() bool { return s.state.fence }

// Write — принять кусок текста и напечатать готовые строки.
func (s *Stream) Write(chunk string) {
	s.pend += chunk
	for {
		i := strings.IndexByte(s.pend, '\n')
		if i < 0 {
			break
		}
		line := s.pend[:i]
		s.pend = s.pend[i+1:]
		s.u.Println(s.u.renderLine(line, s.state))
	}
}

// End — допечатать остаток и закрыть незакрытый блок кода.
func (s *Stream) End() {
	if s.pend != "" {
		s.u.Println(s.u.renderLine(s.pend, s.state))
		s.pend = ""
	}
	if s.state.fence {
		s.u.Println(s.u.Gray(s.u.g.BL + s.u.g.H + s.u.g.H))
		s.state.fence = false
	}
}

// renderLine — отрендерить одну строку markdown.
func (u *UI) renderLine(line string, st *streamState) string {
	trimmed := strings.TrimSpace(line)
	// Маркер блока кода проверяем ДО раннего выхода «мы внутри блока»:
	// иначе закрывающий ``` печатается как обычный текст кода.
	isFence := strings.HasPrefix(trimmed, "```")

	// Внутри блока кода — моноширинный серый, без разметки.
	if st.fence && !isFence {
		return u.c(u.pal.Muted, line)
	}

	if isFence {
		lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
		if !st.fence {
			st.fence = true
			st.lang = lang
			// Блок кода помечен ромбом, как и любое другое событие,
			// а язык — приглушённым хвостом. Иначе строка «◆ ``` go »
			// читается как ещё один заголовок, а кода под ней нет.
			head := "  " + u.emberMark(u.pal.Code)
			if lang != "" {
				head += " " + u.c(u.pal.Muted, lang)
			}
			return head
		}
		st.fence = false
		// Закрывающий зубец: блок «схлопывается» в зубок, а не
		// обводится рамкой, и глаз видит, где код кончился.
		return "  " + u.emberTail()
	}

	if trimmed != "" && reHRule.MatchString(trimmed) {
		return u.c(u.pal.Muted, strings.Repeat(u.g.H, maxInt(10, u.Width()-2)))
	}

	// Разделитель таблицы — скрываем.
	if reTableSep.MatchString(trimmed) {
		return ""
	}

	if m := reH.FindStringSubmatch(line); m != nil {
		level, title := len(m[1]), strings.TrimSpace(m[2])
		if level <= 2 {
			body := u.Bold(u.c(u.pal.Accent, title))
			// Заголовок ответа — ровно как заголовок блока: тот же
			// ромб и та же линия вправо. Иначе он уезжает в край
			// терминала и выглядит как вывод чужой программы.
			n := maxInt(4, minInt(runeLen(title), u.Width()-6))
			return "  " + u.emberMark(u.pal.Accent) + " " + body + " " +
				u.emberRule(n)
		}
		return u.Bold(title)
	}

	if m := reTask.FindStringSubmatch(line); m != nil {
		indent, mark, text := m[1], m[2], m[3]
		box := u.c(u.pal.Muted, "[ ]")
		if strings.EqualFold(mark, "x") {
			box = u.c(u.pal.OK, "["+u.g.Check+"]")
		}
		return indent + box + " " + u.inline(text)
	}

	if m := reOrdered.FindStringSubmatch(line); m != nil {
		indent, num, text := m[1], m[2], m[3]
		return indent + u.c(u.pal.Accent, num+".") + " " + u.inline(text)
	}

	if m := reBullet.FindStringSubmatch(line); m != nil {
		indent, text := m[1], m[2]
		return indent + u.c(u.pal.Accent, u.g.Bullet) + " " + u.inline(text)
	}

	if m := reQuote.FindStringSubmatch(line); m != nil {
		return " " + u.c(u.pal.Muted, u.g.V) + " " + u.Italic(u.c(u.pal.Muted, u.inline(m[1])))
	}

	return u.inline(line)
}

// RenderLine — разовый рендер строки (для истории сообщений).
func (u *UI) RenderLine(line string) string {
	return u.renderLine(line, &streamState{})
}

// RenderText — отрендерить многострочный текст.
func (u *UI) RenderText(text string) string {
	st := &streamState{}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = u.renderLine(l, st)
	}
	return strings.Join(lines, "\n")
}

// inline — обработать inline-разметку с защитой от вложенных замен.
func (u *UI) inline(s string) string {
	var stash []string
	keep := func(rendered string) string {
		stash = append(stash, rendered)
		return "\x00" + itoa(len(stash)-1) + "\x00"
	}

	// 1. Код — раньше всего, содержимое не трогаем.
	s = reCode.ReplaceAllStringFunc(s, func(m string) string {
		inner := m
		if i := strings.Index(inner, "`"); i >= 0 {
			j := strings.LastIndex(inner, "`")
			inner = inner[i+1 : j]
		}
		return keep(u.c(u.pal.Code, "`"+inner+"`"))
	})

	// 2. Ссылки [текст](url).
	s = reLink.ReplaceAllStringFunc(s, func(m string) string {
		p := reLink.FindStringSubmatch(m)
		if p == nil {
			return m
		}
		url := p[2]
		// Длинный URL укорачиваем.
		if runeLen(url) > 34 {
			url = u.c(u.pal.Muted, url[:31]) + u.c(u.pal.Link, "…")
		} else {
			url = u.c(u.pal.Link, url)
		}
		return keep(u.Bold(p[1]) + " " + url)
	})

	// 3. Голые ссылки.
	s = reBareURL.ReplaceAllStringFunc(s, func(m string) string {
		p := reBareURL.FindStringSubmatch(m)
		lead, url := p[1], p[2]
		return lead + keep(u.c(u.pal.Link, "⟨"+url+"⟩"))
	})

	// 4. Жирный, зачёркнутый, курсив.
	// Используем Func-формы: приём с "$1" ломается, потому что "$1"
	// попадает внутрь keep() и уже не раскрывается движком регекспа.
	s = reBoldStar.ReplaceAllStringFunc(s, func(m string) string {
		p := reBoldStar.FindStringSubmatch(m)
		if len(p) < 2 {
			return m
		}
		return keep(u.Bold(p[1]))
	})
	s = reBoldUnder.ReplaceAllStringFunc(s, func(m string) string {
		p := reBoldUnder.FindStringSubmatch(m)
		if len(p) < 2 {
			return m
		}
		return keep(u.Bold(p[1]))
	})
	s = reStrike.ReplaceAllStringFunc(s, func(m string) string {
		p := reStrike.FindStringSubmatch(m)
		if len(p) < 2 {
			return m
		}
		return keep(u.c(u.pal.Muted, "["+u.g.Cross+" "+p[1]+" "+u.g.Cross+"]"))
	})
	s = reItalic.ReplaceAllStringFunc(s, func(m string) string {
		p := reItalic.FindStringSubmatch(m)
		if len(p) < 3 {
			return m
		}
		return p[1] + keep(u.Italic(p[2]))
	})

	// 5. Возврат защищённых фрагментов (вкладываются друг в друга корректно).
	for i := len(stash) - 1; i >= 0; i-- {
		s = strings.ReplaceAll(s, "\x00"+itoa(i)+"\x00", stash[i])
	}
	return s
}

// StripANSI — убрать escape-последовательности (для экспорта и подсчёта ширины).
func StripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\033' {
			inEsc = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// minInt — минимум из двух int.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// VisibleWidth — ширина строки в колонках терминала без ANSI-последовательностей.
func VisibleWidth(s string) int { return visibleWidth(s) }

// PadRight — дополнить строку пробелами до ширины w (с учётом ANSI).
func PadRight(s string, w int) string { return padRight(s, w) }
