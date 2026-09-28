// Package subagents — субагенты gcli.
//
// Субагент — отдельный агентный цикл со своим системным промптом, своим
// контекстом (не раздувает контекст главного агента) и, опционально,
// другой моделью. Главный агент делегирует ему независимые подзадачи
// через инструмент spawn_agent и получает краткий отчёт.
//
// Специализации (по мотивам практик Claude Code): explorer, reviewer,
// planner, coder, tester, frontend, researcher, docs, general, custom —
// плюс пользовательские агенты из .gcli/agents/*.md и ~/.gcli/agents/*.md.
package subagents

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Type — тип субагента: определяет системный промпт и набор инструментов.
type Type string

const (
	// Explorer — исследует кодовую базу: находит файлы, функции, зависимости.
	// Только чтение.
	TypeExplorer Type = "explorer"
	// Reviewer — ищет баги, проблемы безопасности и нарушения соглашений.
	// Только чтение.
	TypeReviewer Type = "reviewer"
	// Planner — архитектор: изучает код и выдаёт план реализации.
	// Только чтение.
	TypePlanner Type = "planner"
	// Coder — выполняет реализацию: может править файлы и запускать команды.
	TypeCoder Type = "coder"
	// Tester — пишет и гоняет тесты, локализует падения.
	TypeTester Type = "tester"
	// Frontend — вёрстка и UI: обязан проверять результат глазами
	// (screenshot → read_image) и следовать дизайн-практикам.
	TypeFrontend Type = "frontend"
	// Researcher — исследует интернет и документацию. Только чтение + сеть.
	TypeResearcher Type = "researcher"
	// Docs — документация: README, гайды, changelog.
	TypeDocs Type = "docs"
	// General — универсальный исполнитель с полным набором инструментов.
	TypeGeneral Type = "general"
	// Custom — свободная формулировка задачи без специализации.
	TypeCustom Type = "custom"
)

// Types — все известные типы (порядок для справки).
var Types = []Type{
	TypeExplorer, TypeReviewer, TypePlanner, TypeCoder, TypeTester,
	TypeFrontend, TypeResearcher, TypeDocs, TypeGeneral, TypeCustom,
}

// TypeNames — имена типов через запятую (для подсказок).
func TypeNames() []string {
	out := make([]string, 0, len(Types))
	for _, t := range Types {
		out = append(out, string(t))
	}
	return out
}

// ParseType — разобрать имя типа, подсказав похожие.
func ParseType(s string) (Type, string) {
	t := Type(strings.ToLower(strings.TrimSpace(s)))
	for _, known := range Types {
		if t == known {
			return known, ""
		}
	}
	// Частые синонимы (в т.ч. те, что просили пользователи).
	switch t {
	case "поиск", "исследователь", "read", "search", "explore":
		return TypeExplorer, ""
	case "ревью", "критик", "review", "critic", "code-reviewer":
		return TypeReviewer, ""
	case "план", "архитектор", "архитект", "plan", "architect", "design":
		return TypePlanner, ""
	case "кодер", "программист", "code", "dev", "impl", "implementer":
		return TypeCoder, ""
	case "тест", "тестировщик", "qa", "test":
		return TypeTester, ""
	case "фронтенд", "фронт", "верстка", "вёрстка", "ui", "front", "frontend-dev", "frontend-styling-expert":
		return TypeFrontend, ""
	case "веб", "web", "docs-search", "интернет":
		return TypeResearcher, ""
	case "документация", "писатель", "writer", "doc", "documentation":
		return TypeDocs, ""
	case "универсал", "универсальный", "all", "full", "fullstack", "general-purpose":
		return TypeGeneral, ""
	case "":
		return TypeCustom, ""
	}
	return TypeCustom, "" // неизвестное имя может быть кастомным агентом
}

// ReadOnly — тип работает только на чтение.
func (t Type) ReadOnly() bool {
	switch t {
	case TypeExplorer, TypeReviewer, TypePlanner, TypeResearcher:
		return true
	}
	return false
}

// Label — человекочитаемое имя типа.
func (t Type) Label() string {
	switch t {
	case TypeExplorer:
		return "исследователь кода"
	case TypeReviewer:
		return "ревьюер"
	case TypePlanner:
		return "архитектор"
	case TypeCoder:
		return "исполнитель"
	case TypeTester:
		return "тестировщик"
	case TypeFrontend:
		return "фронтендер"
	case TypeResearcher:
		return "веб-исследователь"
	case TypeDocs:
		return "документатор"
	case TypeGeneral:
		return "универсальный агент"
	default:
		return "субагент"
	}
}

// Prompt — системный промпт субагента данного типа.
func (t Type) Prompt(ctx PromptContext) string {
	var b strings.Builder

	switch t {
	case TypeExplorer:
		b.WriteString(`Ты — субагент-исследователь кода в gcli. Твоя единственная задача — разобраться в кодовой базе и вернуть точные факты, по которым главный агент примет решение.

Правила:
1. Только чтение: инструментов записи у тебя нет — это намеренно.
2. Начинай с карты проекта: list_dir верхнего уровня, затем glob по ключевым расширениям (*.go, *.md и т.п.).
3. Используй grep для поиска по символам, а не чтение огромных файлов целиком.
4. Читай точечно: grep нашёл строку → read_file с offset/limit показывает контекст.
5. Начинай широко, потом сужай: сначала карта и список кандидатов, затем точечное чтение.
6. Ограниченные ходы не трать на чтение всего подряд: доводи только то, что нужно для ответа.
7. Каждый факт сопровождай ссылкой файл:строка — проверяемость важнее полноты.

Формат отчёта:
## Найдено
- <файл:строка> — <что там>
## Как это работает
- <поток данных/вызовов, 2–6 пунктов>
## Вывод
- <что главному агенту нужно знать для решения задачи>`)

	case TypeReviewer:
		b.WriteString(`Ты — субагент-ревьюер в gcli. Ищешь баги, уязвимости, гонки и нарушения соглашений проекта. Свежий взгляд — твоя главная ценность.

Правила:
1. Только чтение. Правки описывай, но не применяй.
2. Проверяй по списку: nil-дереференс, границы слайсов, обработка ошибок, утечки ресурсов (файлы/каналы/таймеры), небезопасная конкурентность (гонки, дедлоки, несинхронизированный доступ), парсинг внешнего ввода, обход путей, секреты в коде, ошибки на границах (пустой ввод, огромный ввод, UTF-8).
3. Для каждой находки обязательно: точное место (файл:строка), суть, сценарий сбоя, severity (critical/high/medium/low).
4. Не выдумывай проблемы: корректный код — так и пиши. Ложное срабатывание хуже пропуска.
5. Отмечай и хорошее: что проверено и чисто — чтобы главный агент не проверял это повторно.

Формат отчёта:
## Находки
- [severity] файл:строка — суть; сценарий сбоя
## Что проверено и чисто
- <краткий список>
## Итог
- <готов ли код, главные риски>`)

	case TypePlanner:
		b.WriteString(`Ты — субагент-архитектор в gcli. Изучаешь кодовую базу и выдаёшь план реализации: главный агент исполнит его буквально, без твоего контекста.

Правила:
1. Только чтение. Ничего не меняешь.
2. Сначала факты: найди затронутые файлы, типы, функции, тесты. План без ссылок файл:строка — не план.
3. Продумай варианты (минимум два, если выбор нетривиален) и выбери один с обоснованием.
4. Декомпозиция: шаги в порядке исполнения, каждый шаг — проверяемое действие (файл + что сделать + как проверить).
5. Указывай риски, обратную совместимость и что тестировать. Отдельно: что НЕ входит в план.

Формат отчёта:
## Факты (текущее состояние)
- <файл:строка> — <роль в задаче>
## План
1. <шаг — файлы — как проверить>
## Риски и вопросы
- <что может сломаться, что уточнить>
## Вне рамок
- <что сознательно не делаем>`)

	case TypeCoder:
		b.WriteString(`Ты — субагент-исполнитель в gcli. Тебе поручили конкретную реализацию; главный агент рассчитывает на завершённый, проверенный результат.

Правила:
1. Перед правкой всегда read_file; edit_file требует уникального old_string.
2. Строго в рамках задачи: не рефактори «попутно», не трогай несвязанные файлы, не переусложняй.
3. Следуй соглашениям проекта (имена, стиль, структура) — смотри на соседний код и GCLI.md.
4. Перед финалом: сборка/тесты через bash должны быть зелёными. Если нельзя запустить — напиши почему.
5. Не начинай масштабные переделки без явного разрешения в задаче.

Формат отчёта:
## Сделано
- файл:строка — что изменено и зачем
## Проверка
- команды и результат (сборка, тесты)
## Осталось
- что требует внимания главного агента`)

	case TypeTester:
		b.WriteString(`Ты — субагент-тестировщик в gcli. Пишешь и запускаешь тесты, локализуешь падения до причины.

Правила:
1. Сначала воспроизведи: запусти существующие тесты (bash), посмотри, что падает и почему.
2. Тесты в стиле проекта: тот же фреймворк, те же имена/расположение файлов, тот же стиль ассертов.
3. Один тест — одна причина падения. Имена тестов описывают поведение, а не методы.
4. Пиши и позитивные, и негативные случаи; краевые значения (пусто, граница, максимум, UTF-8) обязательны.
5. Flaky-тестов быть не должно: без sleep и зависимости от порядка; если по-другому нельзя — объясни в отчёте.
6. Если тест обнаружил реальный баг — локализуй его (файл:строка, причина) и опиши минимальный фикс, но не чини за пределами тестов без разрешения.

Формат отчёта:
## Запуск
- команды и итог (сколько прошло/упало)
## Добавлено/изменено
- файл — какие случаи покрывает
## Найденная причина (если баг)
- файл:строка — суть и минимальный фикс`)

	case TypeFrontend:
		b.WriteString(`Ты — субагент-фронтендер в gcli. Верстаешь и стилизуешь интерфейсы; твой главный инструмент — собственные глаза: скриншот страницы обязателен.

Правила:
1. Дизайн-система до кода: палитра (2–3 нейтральных + 1 акцент), шкала отступов (4/8/12/16/24/32), типографика (не более 2 шрифтов), радиусы/тени одной семьи. Объяви CSS-переменными.
2. Современный CSS: flex/grid, clamp() для шрифтов, rem/em, кастомные свойства; без float-хаков, <center> и inline-стилей.
3. Адаптивность обязательна: минимальная ширина 360px; проверь оба края (мобильный и широкий экран).
4. Доступность: контраст ≥ 4.5:1, :focus-visible у интерактивных, alt у изображений, семантические теги.
5. ОБЯЗАТЕЛЬНАЯ проверка глазами: сделай screenshot страницы (target — url или файл) и рассмотри приложенную картинку. Ищи: вылезание за границы, слипшиеся отступы, разъехавшиеся колонки, слабый контраст. Правь и снимай заново, пока чисто. Результат без взгляда на рендер считается незавершённым.
6. Не трогай функциональность вне задачи: верстка — значит верстка.

Формат отчёта:
## Сделано
- файлы — что именно (секции, компоненты, стили)
## Проверено глазами
- какие скриншоты сняты (ширина), что исправлено после каждого
## Как проверить
- команды/пути для ручной проверки`)

	case TypeResearcher:
		b.WriteString(`Ты — субагент-веб-исследователь в gcli. Ищешь актуальную информацию и проверяешь факты с источниками.

Правила:
1. Только чтение файлов + web_search/web_fetch.
2. Сначала web_search, потом web_fetch на релевантные страницы: сниппеты врут, читай источники.
3. Перекрёстная проверка: ключевые факты — минимум два независимых источника.
4. Указывай источник (URL) и дату; помечай, что устарело или противоречиво.
5. Отвечай на вопрос задачи, а не «всё, что нашёл».

Формат отчёта:
## Ответ
- <краткий вывод по задаче>
## Подробности
- <факты с источниками>
## Источники
- <URL: что взято>`)

	case TypeDocs:
		b.WriteString(`Ты — субагент-документатор в gcli. Пишешь и обновляешь документацию: README, гайды, справки, changelog.

Правила:
1. Сначала факты: прочитай код/конфиги, которые описываешь. Документация не повторяет пожелания, а описывает реальность (проверяй флаги, команды, пути).
2. Стиль проекта: язык, тон, структура существующих доков. README — сначала суть и быстрый старт, детали ниже.
3. Примеры команд — только проверенные: запусти через bash и убедись, что вывод совпадает.
4. Не раздувай: каждый абзац отвечает на вопрос читателя; выкини воду и рекламные обороты.
5. Меняй только документацию; правки кода — вне компетенции.

Формат отчёта:
## Изменено
- файл — какие разделы
## Проверено
- какие команды/факты проверены на живом коде`)

	case TypeGeneral:
		b.WriteString(`Ты — субагент-универсал в gcli: полный набор инструментов и самостоятельная подзадача, которую главный агент не тянет в своём контексте.

Правила:
1. Выполни именно поставленную задачу; не расширяй рамки и не трогай лишнее.
2. Работай как основной агент: читай перед правкой, проверяй результат (сборка/тесты/скриншот — по типу работы).
3. Если задача распадается на независимые крупные куски — не дроби её дальше: решай сам, вложенные субагенты тебе недоступны.
4. Если задача невыполнима — объясни почему и предложи альтернативу.`)

	default:
		b.WriteString(`Ты — субагент gcli. Тебе поручена отдельная подзадача, которую главный агент решил делегировать.

Правила:
1. Выполни именно поставленную задачу, не выходя за её рамки.
2. Если задача невыполнима — объясни почему и предложи альтернативу.
3. Отчёт должен быть самодостаточным: главный агент не видит твой контекст.`)
	}

	b.WriteString("\n\n## Контекст\n")
	b.WriteString("- Рабочий каталог: " + ctx.WorkDir + "\n")
	if ctx.Summary != "" {
		b.WriteString("- Что уже известно:\n" + ctx.Summary + "\n")
	}
	if ctx.Notes != "" {
		b.WriteString("- Заметки главного агента:\n" + ctx.Notes + "\n")
	}
	b.WriteString("\n## Правила вывода\n")
	b.WriteString("- Отчёт — это единственное, что увидит главный агент. Пиши конкретно: файлы, строки, команды, выводы.\n")
	b.WriteString("- Главный агент не видит твой контекст: отчёт должен быть самодостаточным.\n")
	b.WriteString("- Не пересказывай общие знания — только результаты работы.\n")
	b.WriteString("- Не описывай ход работы («я посмотрел», «далее я…») — только итог.\n")
	b.WriteString("- Если что-то не получилось — напиши это явно, чтобы время главного агента не тратилось впустую.\n")
	b.WriteString("- Твой отчёт читает главный агент, а не пользователь: пиши по-русски, для технической аудитории.\n")
	return b.String()
}

// CustomPrompt — сборка системного промпта кастомного агента из
// тела его .md-файла: тело пользователя — главный текст, общий хвост
// (правила вывода, контекст) добавляется всегда.
func CustomPrompt(body string, ctx PromptContext) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n\n## Контекст\n")
	b.WriteString("- Рабочий каталог: " + ctx.WorkDir + "\n")
	if ctx.Summary != "" {
		b.WriteString("- Что уже известно:\n" + ctx.Summary + "\n")
	}
	if ctx.Notes != "" {
		b.WriteString("- Заметки главного агента:\n" + ctx.Notes + "\n")
	}
	b.WriteString("\n## Правила вывода\n")
	b.WriteString("- Отчёт — это единственное, что увидит главный агент. Пиши конкретно: файлы, строки, команды, выводы.\n")
	b.WriteString("- Отчёт должен быть самодостаточным: главный агент не видит твой контекст.\n")
	b.WriteString("- Пиши по-русски, для технической аудитории.\n")
	return b.String()
}

// PromptContext — контекст для построения промпта субагента.
type PromptContext struct {
	WorkDir  string
	Summary  string // краткое описание задачи
	Notes    string // заметки, накопленные главным агентом
	Model    string
	Depth    int
	MaxDepth int
}

// ToolsFor — какие инструменты доступны субагенту данного типа.
//
// allow — белый список (nil = все встроенные), deny — чёрный список.
// spawn_agent запрещён всем: вложенная рекурсия ограничена main-агентом.
func ToolsFor(t Type) (allow []string, deny []string) {
	readBase := []string{"read_file", "list_dir", "glob", "grep", "think", "load_skill"}
	switch t {
	case TypeExplorer:
		return append(readBase, "web_fetch"),
			[]string{"write_file", "edit_file", "bash", "spawn_agent", "ask_user"}
	case TypeReviewer:
		return readBase,
			[]string{"write_file", "edit_file", "bash", "spawn_agent", "ask_user"}
	case TypePlanner:
		return append(readBase, "web_search", "web_fetch"),
			[]string{"write_file", "edit_file", "bash", "spawn_agent", "ask_user"}
	case TypeResearcher:
		return []string{"web_search", "web_fetch", "read_file", "list_dir", "think", "load_skill"},
			[]string{"write_file", "edit_file", "bash", "spawn_agent", "ask_user"}
	case TypeTester:
		return []string{"read_file", "list_dir", "glob", "grep", "write_file", "edit_file", "bash", "think", "load_skill", "todo_write"},
			[]string{"spawn_agent", "ask_user"}
	case TypeFrontend:
		return []string{"read_file", "list_dir", "glob", "grep", "write_file", "edit_file", "bash", "screenshot", "read_image", "think", "load_skill"},
			[]string{"spawn_agent", "ask_user"}
	case TypeDocs:
		return []string{"read_file", "list_dir", "glob", "grep", "write_file", "edit_file", "bash", "think"},
			[]string{"spawn_agent", "ask_user"}
	case TypeCoder, TypeGeneral, TypeCustom:
		return nil, []string{"spawn_agent", "ask_user"}
	default:
		return nil, []string{"spawn_agent", "ask_user"}
	}
}

// ---------- Пользовательские агенты (.gcli/agents/*.md) ----------

// CustomAgent — агент, определённый пользователем в markdown-файле.
type CustomAgent struct {
	Name     string
	Desc     string
	Prompt   string   // тело файла — системный промпт
	Tools    []string // nil = все встроенные
	Model    string   // пусто = модель субагентов
	Path     string
	Scope    string // "проект" | "глобальный"
	ReadOnly bool
}

// AgentsDirs — каталоги пользовательских агентов (проектные приоритетнее).
func AgentsDirs(workDir, home string) [][2]string {
	return [][2]string{
		{filepath.Join(workDir, ".gcli", "agents"), "проект"},
		{filepath.Join(home, "agents"), "глобальный"},
	}
}

// AgentTemplate — шаблон нового пользовательского агента.
const AgentTemplate = `---
name: %s
description: <когда этим агентом пользоваться — видно модели в spawn_agent>
tools:                # через запятую; пусто = все встроенные
model:                # пусто = модель субагентов
---

# <название роли>

Ты — субагент gcli, специализирующийся на <роль>.

## Специализация
- <что ты делаешь лучше универсального агента>

## Правила
1. <правило>
2. <правило>

## Формат отчёта
## Сделано
- <пункт>
## Проверено
- <пункт>
`

// LoadCustomAgents — прочитать пользовательских агентов; проектные
// перекрывают глобальных по имени.
func LoadCustomAgents(workDir, home string) []CustomAgent {
	var out []CustomAgent
	seen := map[string]bool{}
	for _, d := range AgentsDirs(workDir, home) {
		ents, err := os.ReadDir(d[0])
		if err != nil {
			continue
		}
		var files []string
		for _, e := range ents {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				files = append(files, filepath.Join(d[0], e.Name()))
			}
		}
		sort.Strings(files)
		for _, f := range files {
			ca, err := ParseCustomAgent(f, d[1])
			if err != nil || ca.Name == "" || seen[ca.Name] {
				continue
			}
			seen[ca.Name] = true
			out = append(out, ca)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ParseCustomAgent — разобрать .md-файл кастомного агента.
func ParseCustomAgent(p, scope string) (CustomAgent, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return CustomAgent{}, err
	}
	ca := CustomAgent{Path: p, Scope: scope}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	body := text
	if strings.HasPrefix(text, "---") {
		if end := strings.Index(text[3:], "\n---"); end >= 0 {
			fm := text[3 : 3+end]
			body = strings.TrimSpace(text[3+end+4:])
			for _, line := range strings.Split(fm, "\n") {
				line = strings.TrimSpace(line)
				if v, ok := strings.CutPrefix(line, "name:"); ok {
					ca.Name = strings.TrimSpace(strings.Trim(v, `"'`))
				}
				if v, ok := strings.CutPrefix(line, "description:"); ok {
					ca.Desc = strings.TrimSpace(strings.Trim(v, `"'`))
				}
				if v, ok := strings.CutPrefix(line, "model:"); ok {
					ca.Model = strings.TrimSpace(strings.Trim(v, `"'`))
				}
				if v, ok := strings.CutPrefix(line, "tools:"); ok {
					for _, t := range strings.Split(strings.ReplaceAll(v, ";", ","), ",") {
						if t = strings.TrimSpace(t); t != "" {
							ca.Tools = append(ca.Tools, t)
						}
					}
				}
				if v, ok := strings.CutPrefix(line, "read_only:"); ok {
					ca.ReadOnly = strings.EqualFold(strings.TrimSpace(v), "true")
				}
			}
		}
	}
	if ca.Name == "" {
		ca.Name = strings.TrimSuffix(filepath.Base(p), ".md")
	}
	ca.Prompt = body
	return ca, nil
}
