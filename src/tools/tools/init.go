package tools

import (
	"fmt"
	"path/filepath"
	"strings"

	"gcli/core"
)

// ---------- /init ----------
//
// Команда без аргументов отвечает на вопрос «что это за проект и как его
// собирать». Отвечать приходится текстом, а он почти всегда состоит из
// плейсхолдеров: «<команда сборки>», «<что за проект>». Это худший
// вариант из возможных — человек копирует шаблон, ничего не заполняет,
// и агент читает вместо инструкции пустые угловые скобки.
//
// Проект уже рассказал о себе: go.mod, package.json, Makefile. Здесь эти
// факты превращаются в готовый файл. Ничего не выдумывается — только то,
// что машина прочитала; неизвестное остаётся явным «<…>», чтобы человек
// понял, что это его место для текста.

// InitProfile — что удалось выяснить о проекте.
type InitProfile struct {
	Root        string
	WorkDir     string
	Stacks      []string
	Build       []string
	Test        []string
	EntryPoints []string
	TopDirs     []string
	Notes       []string
}

// InitProfileFor — собрать сведения о проекте для /init.
func (r *Registry) InitProfileFor() InitProfile {
	p := r.ProjectProfile()
	return InitProfile{
		Root:        p.Root,
		WorkDir:     p.WorkDir,
		Stacks:      p.Stacks,
		Build:       p.Build,
		Test:        p.Test,
		EntryPoints: p.EntryPoints,
		TopDirs:     p.TopDirs,
		Notes:       p.Note,
	}
}

// Stack — стек одной строкой.
func (p InitProfile) Stack() string {
	if len(p.Stacks) == 0 {
		return ""
	}
	return strings.Join(p.Stacks, ", ")
}

// RootRel — корень проекта относительно рабочего каталога.
func (p InitProfile) RootRel() string {
	if p.Root == "" {
		return ""
	}
	return core.RelToWD(p.WorkDir, p.Root)
}

// MemoryDoc — содержимое GCLI.md с подставленными фактами о проекте.
//
// Разделы, для которых ничего не нашлось, остаются плейсхолдерами: лучше
// видимая дырка, чем выдуманная команда сборки, на которую агент потом
// потратит вызов.
func MemoryDoc(p InitProfile) string {
	var b strings.Builder

	b.WriteString("# GCLI.md — память проекта для gcli\n\n")
	b.WriteString("> Этот файл подмешивается в системный промпт агента в каждой сессии.\n")
	b.WriteString("> Сгенерирован командой /init: часть данных взята из конфигов проекта,\n")
	b.WriteString("> часть — то, что допишет человек. Строки вида <…> ждут заполнения.\n\n")

	b.WriteString("## О проекте\n")
	if root := p.RootRel(); root != "" && root != "." {
		fmt.Fprintf(&b, "- Корень проекта: %s\n", root)
	}
	if s := p.Stack(); s != "" {
		fmt.Fprintf(&b, "- Стек: %s\n", s)
	}
	b.WriteString("- Назначение: <что это за проект>\n\n")

	b.WriteString("## Команды\n")
	writeList(&b, "Сборка", p.Build, "<команда сборки>")
	writeList(&b, "Тесты", p.Test, "<команда запуска тестов>")
	b.WriteString("- Форматирование: <команда>\n")
	b.WriteString("- Линтер: <команда>\n\n")

	b.WriteString("## Структура\n")
	for _, d := range p.TopDirs {
		if d == "." {
			continue
		}
		fmt.Fprintf(&b, "- %s/ — <что здесь>\n", filepath.ToSlash(d))
	}
	b.WriteString("- <важный каталог> — <что здесь>\n\n")

	b.WriteString("## Правила\n")
	b.WriteString("- <что агенту нельзя делать в этом проекте>\n")
	b.WriteString("- <что требует согласования>\n\n")

	b.WriteString("## Проверка перед сдачей\n")
	if len(p.Test) > 0 {
		fmt.Fprintf(&b, "- Запустить %s\n", p.Test[0])
	} else {
		b.WriteString("- <команда проверки>\n")
	}
	b.WriteString("- <что проверить глазами в интерфейсе, если это применимо>\n\n")

	if len(p.EntryPoints) > 0 {
		b.WriteString("## Точки входа\n")
		for _, e := range p.EntryPoints {
			fmt.Fprintf(&b, "- %s\n", filepath.ToSlash(e))
		}
		b.WriteString("\n")
	}

	if len(p.Notes) > 0 {
		b.WriteString("## Замечания о проекте\n")
		for _, n := range p.Notes {
			fmt.Fprintf(&b, "- %s\n", n)
		}
		b.WriteString("\n")
	}

	return b.String()
}

// writeList — раздел со списком найденного или плейсхолдером.
func writeList(b *strings.Builder, title string, got []string, placeholder string) {
	if len(got) == 0 {
		fmt.Fprintf(b, "- %s: %s\n", title, placeholder)
		return
	}
	for _, g := range got {
		fmt.Fprintf(b, "- %s: `%s`\n", title, g)
	}
}

// PermissionRules — правила разрешений, выведенные из стека проекта.
//
// Правила намеренно узкие и осторожные: разрешается ровно то, что агент
// обязан делать постоянно (сборка, тесты, чтение файлов проекта), а всё
// разрушительное и внешнее остаётся вопросом. Запреты — только там, где
// ошибка стоит дорого и необратимо: чужие ключи и .env никогда не нужны
// для работы с кодом, а `rm -rf` без вопроса — это потеря проекта.
//
// Общие правила идут первыми, частные — после: порядок значим, последнее
// совпавшее выигрывает.
func PermissionRules(p InitProfile) []string {
	rules := []string{
		// Чтение и запись внутри проекта — основа работы агента.
		"read_file: allow",
		"grep: allow",
		"glob: allow",
		"edit: allow",
	}
	// seen убирает дубли: у Go-проекта «go build ./...» и «go build .»
	// дают один и тот же шаблон, а в список попадали бы дважды.
	seen := map[string]bool{}
	if root := p.RootRel(); root != "" && root != "." {
		// Правила пишутся от корня проекта, а подтверждение получает
		// абсолютный путь — приведение к относительному делает движок.
		rules = append(rules,
			"edit("+root+"/**): allow",
			"read_file("+root+"/**): allow",
		)
	}
	// Секреты в коде задачи не решают.
	rules = append(rules,
		"read_file(**/.env): deny",
		"read_file(**/*.pem): deny",
		"read_file(**/id_rsa*): deny",
	)
	// Команды стека: то, что агент делает на каждом шаге.
	for _, c := range append(append([]string{}, p.Build...), p.Test...) {
		if pat := cmdPattern(c); !seen[pat] {
			seen[pat] = true
			rules = append(rules, "bash("+pat+"): allow")
		}
	}
	switch {
	case hasStack(p.Stacks, "Go"):
		seen["go vet*"] = true
		seen["gofmt*"] = true
		rules = append(rules, "bash(go vet*): allow", "bash(gofmt*): allow", "bash(go run*): ask")
	case hasStack(p.Stacks, "Node"):
		rules = append(rules, "bash(npm run*): allow", "bash(npm publish*): ask")
	case hasStack(p.Stacks, "Rust"):
		seen["cargo build*"] = true
		seen["cargo test*"] = true
		rules = append(rules, "bash(cargo build*): allow", "bash(cargo test*): allow", "bash(cargo clippy*): allow")
	case hasStack(p.Stacks, "Python"):
		rules = append(rules, "bash(pytest*): allow", "bash(python -m pytest*): allow")
	}
	// Разрушительное и внешнее — вопрос, всегда.
	rules = append(rules,
		"bash(rm *): ask",
		"bash(git push*): ask",
		"bash(curl *): ask",
		"web_fetch: ask",
	)
	return rules
}

// cmdPattern — шаблон команды для правила разрешений.
//
// Команда разбирается на имя программы и подкоманду, чтобы правило
// разрешало работу, а не одно дословное совпадение:
//
//	go build ./... → go build*
//	go test ./...  → go test*
//	just test      → just test*
//
// Почему не «первое слово плюс звёздочка» (go*): такое правило разрешает
// и go mod tidy, и go get, а это уже изменение зависимостей — об этом
// человек не писал, он писал «можно собирать». Подкоманда входит в
// шаблон всегда, аргументы после неё — уже свободны.
func cmdPattern(cmd string) string {
	fields := strings.Fields(strings.TrimSpace(cmd))
	switch len(fields) {
	case 0:
		return "*"
	case 1:
		return globName(fields[0])
	}
	// Многословная команда («just test», «npm run build»): берём два слова.
	//
	// Звёздочка ставится только на подкоманду, НЕ на имя программы: «go*
	// build*» разрешило бы и «go mod build», а «npm* run*» — что угодно
	// начинающееся с npm. Имя программы пишется дословно.
	return fields[0] + " " + globName(fields[1])
}

// globName — слово с хвостом-звёздочкой, если в нём ещё нет wildcard.
func globName(s string) string {
	if strings.ContainsAny(s, "*?") {
		return s
	}
	return s + "*"
}

// hasStack — есть ли стек в списке.
func hasStack(stacks []string, want string) bool {
	for _, s := range stacks {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}
