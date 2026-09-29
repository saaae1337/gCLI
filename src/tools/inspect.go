package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"gcli/core"
)

// ---------- inspect ----------
//
// edit_file отказывается работать, если old_string не совпал. Обычно это
// читаемая ошибка — но иногда несовпадение невидимо: таб против пробела,
// неразрывный пробел, BOM в начале файла, CRLF против LF. Модель смотрит на
// две почти одинаковые строки, не видит разницы и тратит вызовы впустую:
// перечитывает файл, меняет отступы наугад, снова получает тот же отказ.
//
// inspect показывает файл в виде, где отличия видно: табы видны как →, конец
// строки помечен, невидимые символы названы. Один вызов заменяет пять.

// EscapeLine — строка с видимыми escape-последовательностями.
func EscapeLine(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\t':
			b.WriteString("→") // таб: главный источник расхождений с edit_file
		case '\r':
			b.WriteString("␍") // CR: признак CRLF-окончаний строк
		case '\u00a0':
			b.WriteString("⍽") // неразрывный пробел
		case '\u200b':
			b.WriteString("␣") // zero-width space
		case ' ':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// InvisibleReport — что невидимого нашлось в файле.
type InvisibleReport struct {
	Path      string
	HasBOM    bool
	CRLF      bool
	LF        int
	CRLFCount int
	Tabs      int
	Trailing  []int // строки с пробелами в конце
	Width     int
}

// DetectInvisible — найти невидимое в содержимом файла.
func DetectInvisible(path, content string) InvisibleReport {
	rep := InvisibleReport{Path: path, CRLF: strings.Contains(content, "\r\n")}
	rep.HasBOM = strings.HasPrefix(content, "\ufeff")
	for i, l := range core.SplitLines(content) {
		if strings.Contains(l, "\t") {
			rep.Tabs++
		}
		if len(l) > 0 && (strings.HasSuffix(l, " ") || strings.HasSuffix(l, "\t")) {
			rep.Trailing = append(rep.Trailing, i+1)
		}
	}
	rep.CRLFCount = strings.Count(content, "\r\n")
	rep.LF = strings.Count(content, "\n")
	if rep.CRLF {
		rep.Width = 2
	} else {
		rep.Width = 1
	}
	return rep
}

// Text — отчёт о невидимом.
func (r InvisibleReport) Text() string {
	var b strings.Builder
	if r.HasBOM {
		b.WriteString("⚠ В начале файла BOM (U+FEFF) — он часть первой строки, edit_file по ней не совпадёт.\n")
	}
	if r.CRLF {
		b.WriteString(fmt.Sprintf("⚠ Окончания строк CRLF (%d шт): после «строки» есть скрытый CR. "+
			"В ответе edit_file пиши LF, иначе не совпадёт.\n", r.CRLFCount))
	}
	if r.Tabs > 0 {
		b.WriteString(fmt.Sprintf("Табов в строках: %d — в выводе ниже они показаны как →.\n", r.Tabs))
	}
	if len(r.Trailing) > 0 {
		show := r.Trailing
		if len(show) > 5 {
			show = show[:5]
		}
		b.WriteString(fmt.Sprintf("Пробелы в конце строк: %d (строки %s) — edit_file по ним не сойдётся.\n",
			len(r.Trailing), joinInts(show)))
	}
	if b.Len() == 0 {
		b.WriteString("Невидимых символов не найдено: отступы обычные, окончания строк LF.\n")
	}
	return strings.TrimSpace(b.String())
}

func joinInts(v []int) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ", ")
}

// hInspect — точный вид фрагмента файла.
func (r *Registry) hInspect(_ context.Context, m map[string]any) (Result, error) {
	raw := ArgStr(m, "path")
	if raw == "" {
		return Result{}, fmt.Errorf("укажи path")
	}
	p := r.resolvePath(raw)
	data, err := readFileSafe(p)
	if err != nil {
		return Result{}, err
	}
	markRead(r.env.ReadFiles, p)
	content := string(data)
	lines := core.SplitLines(content)

	rep := DetectInvisible(p, content)
	var b strings.Builder
	fmt.Fprintf(&b, "# Точное содержимое: %s\n\n%s\n\n", rep.Path, rep.Text())

	// Участок: либо по номерам строк, либо вокруг искомого текста.
	off, lim := ArgInt(m, "offset", 1), ArgInt(m, "limit", 40)
	if lim < 1 || lim > 500 {
		lim = 40
	}
	if s := ArgStr(m, "search"); s != "" {
		idx := 0
		for i, l := range lines {
			if strings.Contains(l, s) {
				idx = i
				break
			}
		}
		from := core.Max(0, idx-3)
		to := core.Min(len(lines), idx+4)
		fmt.Fprintf(&b, "## Совпадения для %q (строки %d–%d)\n", s, from+1, to)
		for i := from; i < to; i++ {
			fmt.Fprintf(&b, "%5d │ %s\n", i+1, EscapeLine(lines[i]))
		}
		return Result{Text: b.String(), Summary: fmt.Sprintf("escape-вид: %s", core.RelToWD(r.workDir, p))}, nil
	}

	off = core.Max(1, off)
	to := core.Min(len(lines), off-1+lim)
	if off > len(lines) {
		return Result{
			Text:    fmt.Sprintf("В файле %d строк, запрошенная строка %d за пределами.", len(lines), off),
			Summary: "вне диапазона",
		}, nil
	}
	b.WriteString("## Строки\n")
	for i := off - 1; i < to; i++ {
		fmt.Fprintf(&b, "%5d │ %s\n", i+1, EscapeLine(lines[i]))
	}
	b.WriteString("\n↑ Этот вид — для ДИАГНОСТИКИ, а не для копирования: символ → означает настоящий таб,\n" +
		"в old_string нужен реальный символ, а не стрелка. Нужен escape-вид ровно тогда, когда\n" +
		"edit_file не находит old_string: здесь видно, чем он отличается от того, что в файле.")
	return Result{
		Text:    strings.TrimSpace(b.String()),
		Summary: fmt.Sprintf("escape-вид %d строк", to-off+1),
	}, nil
}
