package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gcli/core"
)

// ---------- Проверка HTML-страницы ----------

// Страница без сборочного конвейера (лендинг, статическая страница) нечем
// проверять: тестов нет, сборки нет, а «просто открыть и посмотреть»
// ничего не говорит о битых ссылках и забытых якорях. Проверка ниже
// закрывает именно этот случай: она ловит то, что ломает страницу тихо.

// PageReport — результат проверки HTML-страницы.
type PageReport struct {
	Path     string
	Checks   int
	Problems []string
}

// Text — отчёт для контекста модели.
func (r PageReport) Text() string {
	if len(r.Problems) == 0 {
		return fmt.Sprintf("Страница %s: проблем не найдено (проверок: %d).", r.Path, r.Checks)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Страница %s: проблем — %d (проверок: %d)\n", r.Path, len(r.Problems), r.Checks)
	for _, p := range r.Problems {
		b.WriteString("- " + p + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// Summary — короткий итог для карточки инструмента.
func (r PageReport) Summary() string {
	if len(r.Problems) == 0 {
		return fmt.Sprintf("страница в порядке (%d проверок)", r.Checks)
	}
	return fmt.Sprintf("страница: проблем — %d", len(r.Problems))
}

// Регулярки разбора HTML. Полноценного парсера нет (зависимостей у
// проекта нет вообще), но для этих проверок хватает: нужны адреса
// ресурсов, якоря и идентификаторы.
var (
	reHTMLHref  = regexp.MustCompile(`(?i)\bhref\s*=\s*["']([^"']+)["']`)
	reHTMLSrc   = regexp.MustCompile(`(?i)\bsrc\s*=\s*["']([^"']+)["']`)
	reHTMLID    = regexp.MustCompile(`(?i)\bid\s*=\s*["']([^"']+)["']`)
	reHTMLTitle = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reHTMLAlt   = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	reHTMLAltOK = regexp.MustCompile(`(?is)<img\b[^>]*\balt\s*=\s*["'][^"']*["']`)
)

// checkHTMLPage — проверить HTML-страницу: битые ресурсы, мёртвые якоря,
// отсутствующий title и картинки без alt.
func checkHTMLPage(path string) PageReport {
	rep := PageReport{Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		rep.Problems = append(rep.Problems, "файл не читается: "+err.Error())
		return rep
	}
	html := string(data)
	dir := filepath.Dir(path)

	// Структура документа.
	for _, tag := range []string{"<html", "<head", "<body"} {
		rep.Checks++
		if !strings.Contains(strings.ToLower(html), tag) {
			rep.Problems = append(rep.Problems, "нет "+tag+">")
		}
	}

	// Заголовок страницы: без него вкладка в браузере пустая, а в
	// поиске страница не находится.
	rep.Checks++
	title := strings.TrimSpace(reHTMLTitle.ReplaceAllString(html, "$1"))
	if title == "" {
		rep.Problems = append(rep.Problems, "нет непустого <title>")
	}

	// Мобильный: без viewport телефон покажет desktop-версию.
	rep.Checks++
	if !strings.Contains(strings.ToLower(html), `name="viewport"`) &&
		!strings.Contains(strings.ToLower(html), `name='viewport'`) {
		rep.Problems = append(rep.Problems, "нет <meta name=\"viewport\"> — на телефоне страница откроется во всю ширину")
	}

	// Идентификаторы — чтобы проверить якоря.
	ids := map[string]bool{}
	for _, m := range reHTMLID.FindAllStringSubmatch(html, -1) {
		ids[m[1]] = true
	}

	// Локальные ресурсы: существуют ли они.
	seen := map[string]bool{}
	for _, m := range append(reHTMLHref.FindAllStringSubmatch(html, -1), reHTMLSrc.FindAllStringSubmatch(html, -1)...) {
		ref := strings.TrimSpace(m[1])
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		switch {
		case strings.HasPrefix(ref, "#"):
			// Внутренний якорь: должен быть элемент с таким id.
			rep.Checks++
			id := strings.TrimPrefix(ref, "#")
			if id != "" && !ids[id] {
				rep.Problems = append(rep.Problems, "якорь #"+id+" — элемента с таким id нет")
			}
		case strings.Contains(ref, "://"), strings.HasPrefix(ref, "//"),
			strings.HasPrefix(ref, "mailto:"), strings.HasPrefix(ref, "data:"),
			strings.HasPrefix(ref, "#"):
			// Внешний адрес — проверять нечем.
		case strings.HasPrefix(ref, "/"):
			// Абсолютный путь от корня сайта: относительно проекта
			// неизвестен, пропускаем.
		default:
			rep.Checks++
			p := filepath.Join(dir, filepath.FromSlash(ref))
			if _, err := os.Stat(p); err != nil {
				rep.Problems = append(rep.Problems, "ресурс не найден: "+ref)
			}
		}
	}

	// Картинки без alt: для незрячих пользователей это пустые места.
	for _, img := range reHTMLAlt.FindAllString(html, -1) {
		rep.Checks++
		if !reHTMLAltOK.MatchString(img) {
			rep.Problems = append(rep.Problems, "<img> без alt: "+core.Truncate(core.OneLine(img), 80))
		}
	}

	return rep
}

// findHTMLPage — найти HTML-страницу проекта: index.html в корне или
// единственная страница в каталоге.
func findHTMLPage(workDir string) string {
	cands := []string{"index.html", "main.html", "page.html", "home.html"}
	for _, c := range cands {
		p := filepath.Join(workDir, c)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	// Страница в единственном html-файле проекта.
	var found []string
	_ = filepath.WalkDir(workDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(p), ".html") {
			found = append(found, p)
		}
		if len(found) > 1 {
			return filepath.SkipAll
		}
		return nil
	})
	if len(found) == 1 {
		return found[0]
	}
	return ""
}
