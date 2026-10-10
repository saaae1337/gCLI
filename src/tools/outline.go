package tools

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"

	"gcli/core"
)

// ---------- outline ----------
//
// list_dir отвечает на вопрос «что тут лежит», read_file — «что внутри».
// Между ними пусто: «что объявлено в этом файле и на какой строке».
// Агенту приходится отвечать на него самому, и единственный способ —
// прочитать файл целиком. Для файла на две тысячи строк это примерно
// двадцать пять тысяч токенов, чтобы увидеть список из сорока имён.
//
// Дальше с этим списком работают хуже всего на свежем контексте: имена
// без сигнатур и без строк бесполезны, а с ними — карта проекта, по
// которой можно прыгнуть в нужное место одним read_file с offset.
//
// Для Go разбор точный: go/parser из стандартной библиотеки. Остальные
// языки разбираются эвристически по регуляркам, и про это сказано в
// самом выводе — иначе агент поверит и построит план на том, чего в
// файле нет.

// outlineMaxSymbols — потолок объявлений в ответе. Обрезанный список
// объявляется явно: молчаливое усечение читается как «объявлений
// больше нет», и агент заключит, что файл устроен проще, чем он есть.
const outlineMaxSymbols = 120

// outlineMaxWidth — сколько символов сигнатуры показывать.
const outlineMaxWidth = 160

// decl — одно объявление: где, что и зачем.
type decl struct {
	line int    // строка объявления, 1-based
	sig  string // сигнатура или первая строка объявления
	doc  string // первая строка документации, если есть
}

// langRule — эвристика для языка без точного разбора.
type langRule struct {
	// re ловит объявление в начале строки; последняя группа — имя.
	re *regexp.Regexp
}

// langRules — эвристики по расширениям.
//
// Регулярки намеренно строгие к началу строки: иначе в вывод попадают
// вызовы и комментарии, и агент получает список, в котором половина
// строк — мусор. Это лучше, чем пустой список: мусор виден глазом.
var langRules = map[string][]langRule{
	".py": {
		{regexp.MustCompile(`^\s*async\s+def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)},
		{regexp.MustCompile(`^\s*def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)},
		{regexp.MustCompile(`^\s*class\s+([A-Za-z_][A-Za-z0-9_]*)\s*[:(]`)},
	},
	".ts":  tsRules(),
	".tsx": tsRules(),
	".js":  tsRules(),
	".jsx": tsRules(),
	".mjs": tsRules(),
	".rs": {
		{regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?(?:unsafe\s+)?fn\s+([A-Za-z_][A-Za-z0-9_]*)`)},
		{regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?struct\s+([A-Za-z_][A-Za-z0-9_]*)`)},
		{regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?enum\s+([A-Za-z_][A-Za-z0-9_]*)`)},
		{regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?trait\s+([A-Za-z_][A-Za-z0-9_]*)`)},
		{regexp.MustCompile(`^\s*impl(?:<[^>]*>)?\s+([A-Za-z_][A-Za-z0-9_:<>]*)`)},
	},
	".java": {
		{regexp.MustCompile(`^\s*(?:public|private|protected)?\s*(?:static\s+)?(?:final\s+)?(?:abstract\s+)?class\s+([A-Za-z_][A-Za-z0-9_]*)`)},
		{regexp.MustCompile(`^\s*(?:public|private|protected)?\s*(?:static\s+)?interface\s+([A-Za-z_][A-Za-z0-9_]*)`)},
		{regexp.MustCompile(`^\s*(?:public|private|protected)?\s*enum\s+([A-Za-z_][A-Za-z0-9_]*)`)},
		{regexp.MustCompile(`^\s*(?:public|private|protected)\s+(?:static\s+)?(?:final\s+)?[A-Za-z_<>\[\],. ]+\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)},
	},
}

// tsRules — объявления TypeScript и JavaScript.
func tsRules() []langRule {
	return []langRule{
		{regexp.MustCompile(`^\s*export\s+default\s+(?:async\s+)?(?:function|class)\s+([A-Za-z_$][\w$]*)`)},
		{regexp.MustCompile(`^\s*export\s+(?:declare\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)`)},
		{regexp.MustCompile(`^\s*export\s+(?:declare\s+)?class\s+([A-Za-z_$][\w$]*)`)},
		{regexp.MustCompile(`^\s*export\s+(?:declare\s+)?(?:interface|type|enum)\s+([A-Za-z_$][\w$]*)`)},
		{regexp.MustCompile(`^\s*export\s+(?:declare\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)`)},
		{regexp.MustCompile(`^\s*(?:async\s+)?function\s+([A-Za-z_$][\w$]*)`)},
		{regexp.MustCompile(`^\s*class\s+([A-Za-z_$][\w$]*)`)},
		{regexp.MustCompile(`^\s*(?:interface|type|enum)\s+([A-Za-z_$][\w$]*)`)},
		// Стрелочные функции в константе: самая частая форма в modern TS/JS.
		{regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::[^=]+)?=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*(?::[^=]*)?=>`)},
	}
}

// outlineGo — объявления Go через go/parser.
//
// Разбор может упасть: файл часто читают посреди правки, и синтаксис в
// этот момент может быть негодным. Частичное дерево при этом всё равно
// годится — parser возвращает всё, до чего дошёл, — поэтому ошибка не
// мешает, а лишь помечается в ответе.
func outlineGo(path, src string) ([]decl, bool) {
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, path, src, parser.ParseComments|parser.SkipObjectResolution)
	if f == nil {
		return nil, false
	}
	var out []decl
	add := func(pos token.Pos, sig, doc string) {
		out = append(out, decl{line: fset.Position(pos).Line, sig: clip(sig, outlineMaxWidth), doc: doc})
	}
	for _, d := range f.Decls {
		switch decl := d.(type) {
		case *ast.FuncDecl:
			name := decl.Name.Name
			if decl.Recv != nil && len(decl.Recv.List) > 0 {
				name = recvString(decl.Recv) + " " + name
			}
			sig := "func " + name + paramsOf(decl.Type.Params) + resultsOf(decl.Type.Results)
			add(decl.Pos(), sig, trimDocPrefix(docFirst(decl.Doc), decl.Name.Name))
		case *ast.GenDecl:
			kind := decl.Tok.String()
			for _, s := range decl.Specs {
				switch spec := s.(type) {
				case *ast.TypeSpec:
					add(spec.Pos(), "type "+spec.Name.Name+" "+typeHead(spec.Type),
						trimDocPrefix(docFirst(decl.Doc), spec.Name.Name))
				case *ast.ValueSpec:
					doc := docFirst(spec.Doc)
					if doc == "" {
						doc = docFirst(decl.Doc)
					}
					for _, n := range spec.Names {
						add(n.Pos(), kind+" "+n.Name, trimDocPrefix(doc, n.Name))
					}
				}
			}
		}
	}
	return out, true
}

// recvString — получатель с именем переменной: «(w *Widget)», а не «(*Widget)».
//
// Имя важно: по нему видно, что метод работает с копией или с указателем,
// а «(*Widget)» эту разницу стирает — и выглядит как оригинал, хотя
// отличается от него.
func recvString(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return "()"
	}
	return "(" + fieldsText(recv) + ")"
}

func paramsOf(params *ast.FieldList) string {
	if params == nil {
		return "()"
	}
	return "(" + fieldsText(params) + ")"
}

// resultsOf — результаты так, как их пишут в Go: одиночный безымянный —
// без скобок, остальные — в скобках. «)(error)» выглядит как опечатка,
// и на такой вывод агент тратит внимание зря.
func resultsOf(results *ast.FieldList) string {
	if results == nil || len(results.List) == 0 {
		return ""
	}
	if len(results.List) == 1 && len(results.List[0].Names) == 0 {
		return " " + exprString(results.List[0].Type)
	}
	return " (" + fieldsText(results) + ")"
}

// trimDocPrefix — убрать из описания имя, которое уже есть в сигнатуре.
//
// В Go описание начинается с имени по соглашению: «Widget — то, что
// показывают». Рядом с сигнатурой это даёт «Widget — Widget — то, что
// показывают», а повтор на каждой строке съедает смысл и токены.
func trimDocPrefix(doc, name string) string {
	if doc == "" || name == "" {
		return doc
	}
	rest := strings.TrimPrefix(doc, name)
	if rest == doc {
		return doc
	}
	// Отрезать можно только разделитель после имени: «Widgetize» и
	// «WidgetStore» описывают другие вещи и трогать их нельзя.
	for _, sep := range []string{" — ", " – ", " - ", ": ", " —", " -"} {
		if strings.HasPrefix(rest, sep) {
			return strings.TrimSpace(strings.TrimPrefix(rest, sep))
		}
	}
	return doc
}

func fieldsText(fl *ast.FieldList) string {
	parts := make([]string, 0, len(fl.List))
	for _, f := range fl.List {
		typ := exprString(f.Type)
		if len(f.Names) == 0 {
			parts = append(parts, typ)
			continue
		}
		names := make([]string, 0, len(f.Names))
		for _, n := range f.Names {
			names = append(names, n.Name)
		}
		parts = append(parts, strings.Join(names, ", ")+" "+typ)
	}
	return strings.Join(parts, ", ")
}

// exprHead — короткое имя типа без внутренностей: *Registry, []core.Image,
// map[string]any. Полный текст выражения здесь не нужен и только шумит.
func exprString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprString(t.X)
	case *ast.SelectorExpr:
		return exprString(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + exprString(t.Elt)
	case *ast.MapType:
		return "map[" + exprString(t.Key) + "]" + exprString(t.Value)
	case *ast.InterfaceType:
		return "any"
	case *ast.StructType:
		return "struct"
	case *ast.FuncType:
		return "func" + paramsOf(t.Params) + resultsOf(t.Results)
	case *ast.Ellipsis:
		return "..." + exprString(t.Elt)
	case *ast.ChanType:
		return "chan " + exprString(t.Value)
	}
	return "…"
}

// typeHead — вид объявления типа: у struct и interface — «{».
func typeHead(e ast.Expr) string {
	switch e.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	}
	return exprString(e)
}

// docFirst — первая содержательная строка документации над объявлением.
func docFirst(g *ast.CommentGroup) string {
	if g == nil {
		return ""
	}
	for _, c := range g.List {
		txt := strings.TrimSpace(c.Text)
		txt = strings.TrimPrefix(txt, "//")
		txt = strings.TrimPrefix(txt, "/*")
		txt = strings.TrimSuffix(txt, "*/")
		txt = strings.TrimSpace(txt)
		// Служебные пометки и пустые строки описанием не считаются.
		if txt == "" || strings.HasPrefix(txt, "nolint") || strings.HasPrefix(txt, "go:") {
			continue
		}
		if i := strings.IndexByte(txt, '\n'); i >= 0 {
			txt = strings.TrimSpace(txt[:i])
		}
		return clip(txt, 110)
	}
	return ""
}

// outlineHeuristic — объявления по регуляркам, для языков без точного
// разбора.
//
// В вывод идёт сама строка объявления, а не то, что осталось от неё
// после вырезания совпадения: остаток — это «path):», из которого
// ничего не прочтёшь. Строка целиком — это ровно то, что человек видит
// в редакторе на той же строке.
func outlineHeuristic(ext string, lines []string) []decl {
	rules := langRules[ext]
	var out []decl
	for i, line := range lines {
		if len(line) > 400 {
			line = line[:400]
		}
		for _, ru := range rules {
			if ru.re.FindStringSubmatch(line) == nil {
				continue
			}
			out = append(out, decl{
				line: i + 1,
				sig:  clip(collapseWS(line), outlineMaxWidth),
				doc:  docLineAbove(lines, i),
			})
			break
		}
	}
	return out
}

// docLineAbove — строка комментария «#» или «//» прямо над объявлением.
func docLineAbove(lines []string, i int) string {
	for j := i - 1; j >= 0 && j >= i-3; j-- {
		t := strings.TrimSpace(lines[j])
		if t == "" {
			continue
		}
		for _, p := range []string{"//", "#", "///", "*"} {
			if strings.HasPrefix(t, p) {
				return clip(strings.TrimSpace(strings.TrimLeft(t, "/*#")), 110)
			}
		}
		return ""
	}
	return ""
}

func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

// outlineLang — человеческое имя языка для заголовка ответа.
func outlineLang(ext string) (string, bool) {
	switch ext {
	case ".go":
		return "Go", true
	case ".py":
		return "Python", true
	case ".ts":
		return "TypeScript", true
	case ".tsx":
		return "TypeScript/JSX", true
	case ".js", ".jsx", ".mjs":
		return "JavaScript", true
	case ".rs":
		return "Rust", true
	case ".java":
		return "Java", true
	}
	return "", false
}

// hOutline — структура файла: объявления с номерами строк.
func (r *Registry) hOutline(_ context.Context, m map[string]any) (Result, error) {
	raw := ArgStr(m, "path")
	if raw == "" {
		return Result{}, fmt.Errorf("укажи path")
	}
	p, err := r.pathArg(raw)
	if err != nil {
		return Result{}, err
	}
	rel := core.RelToWD(r.workDir, p)
	ext := strings.ToLower(filepath.Ext(p))
	lang, ok := outlineLang(ext)
	if !ok {
		return Result{
			Text: fmt.Sprintf("Язык %s не разбирается (%s).\n"+
				"Точный разбор есть для Go, эвристика — для Python, TypeScript, JavaScript, "+
				"Rust и Java.\nДля остальных ищи объявления через grep.",
				ext, rel),
			Summary: "язык не разбирается",
		}, nil
	}

	data, _, truncated, err := readFileLimited(p, maxInspectBytes)
	if err != nil {
		return Result{}, err
	}
	src := string(data)
	lines := core.SplitLines(src)

	// Для Go разбор точный, для остальных — эвристика. Разница сказана
	// прямо в ответе: агент строит на списке план работы, и доверять
	// надо разным объёмам правды.
	exactness := "эвристика по регуляркам: возможны пропуски и лишние строки"
	var decls []decl
	if ext == ".go" {
		exactness = "точный разбор (go/parser)"
		var parsed bool
		decls, parsed = outlineGo(p, src)
		if !parsed || len(decls) == 0 {
			exactness = "разбор не дал объявлений — файл, возможно, негодный"
		}
	} else {
		decls = outlineHeuristic(ext, lines)
	}

	// Фильтр по имени: найти нужное в файле на две тысячи строк, не
	// листая весь список.
	if f := strings.TrimSpace(ArgStr(m, "filter")); f != "" {
		lf := strings.ToLower(f)
		var kept []decl
		for _, d := range decls {
			if strings.Contains(strings.ToLower(d.sig), lf) ||
				strings.Contains(strings.ToLower(d.doc), lf) {
				kept = append(kept, d)
			}
		}
		decls = kept
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Структура: %s\n%s, %d строк, объявлений: %d — %s\n",
		rel, lang, len(lines), len(decls), exactness)
	if truncated {
		fmt.Fprintf(&b, "[внимание: файл больше %s, разобран только его начало]\n",
			core.HumanSize(maxInspectBytes))
	}
	if len(decls) == 0 {
		b.WriteString("\nОбъявлений не найдено.")
		if ext == ".go" {
			b.WriteString(" У go/parser нечего показать — файл не разбирается или пуст.")
		} else {
			b.WriteString(" Эвристика могла не узнать синтаксис — проверь файл через read_file.")
		}
		return Result{Text: b.String(), Summary: "объявлений нет"}, nil
	}

	b.WriteByte('\n')
	shown := decls
	cut := false
	if len(shown) > outlineMaxSymbols {
		shown, cut = shown[:outlineMaxSymbols], true
	}
	for _, d := range shown {
		fmt.Fprintf(&b, "%6d │ %s", d.line, d.sig)
		if d.doc != "" {
			fmt.Fprintf(&b, "  — %s", d.doc)
		}
		b.WriteByte('\n')
	}
	if cut {
		fmt.Fprintf(&b, "\n[показано %d из %d — сузь список через filter]\n", len(shown), len(decls))
	}
	b.WriteString("\nНомера строк — для read_file с offset, чтобы открыть нужное место целиком.\n" +
		"Это карта, а не содержание: тел функций здесь нет, и судить о поведении по ней нельзя.")
	return Result{
		Text:    b.String(),
		Summary: fmt.Sprintf("%s: %d объявлений", filepath.Base(p), len(decls)),
	}, nil
}
