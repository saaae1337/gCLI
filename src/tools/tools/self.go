package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gcli/core"
)

// SelfReport — снимок собственного состояния агента.
//
// Инструмент существует ради одной идеи: модель не может работать хорошо,
// если не видит себя. До его появления расход токенов, размер контекста,
// список уже прочитанных файлов и активные субагенты были видны только
// человеку через /status — то есть оставались вне поля зрения агента.
// Видимость состояния — самая дешёвая из возможных оптимизаций: агент
// начинает экономить токены сам, замечает приближение порога сжатия
// контекста и перестаёт повторно читать один и тот же файл.
type SelfReport struct {
	// Актуальный промпт агента (нужен, чтобы посчитать его вес).
	// Заполняется хостом перед вызовом инструмента.
	SystemPrompt string
	// Порог авто-сжатия контекста (0 = сжатие выключено).
	CompactAt int
	// Лимит итераций агентного цикла.
	MaxIters int
	// Модель и провайдер.
	Model    string
	Provider string
	// Режим: главный агент или субагент.
	Subagent string
	Depth    int
	// Собственная статистика.
	Usage core.Usage
	Stats core.Stats
	// Сколько ходов агентного цикла уже выполнено в этом ходе.
	Turns int
	// ExtendAbs — абсолютный потолок итераций с продлениями.
	ExtendAbs int
	// Extends — сколько раз ход уже продлили за этот ход.
	Extends int
	// История диалога.
	History []core.Message
	// Сколько файлов агент уже прочитал (для edit_file нужно было прочитать).
	ReadFiles int
	// Заметки по задаче (task_note).
	Notes []string
	// Сводка по субагентам.
	Agents string
	// Mission — строка об автономном прогоне (пусто, если прогона нет).
	//
	// Отдельное поле, а не часть системного промпта: блок миссии в промпте
	// один и статичен, а прогон живёт — и модель обязана видеть, сколько
	// раз её уже просили продолжать, иначе на длинной дистанции она
	// продолжает одно и то же по кругу, не подозревая, что её одержимо.
	Mission string
	// Активен ли режим чтения только.
	ReadOnly bool
	// Время, прошедшее с начала сессии.
	Since time.Duration
}

// toolLimits — фактические лимиты инструментов.
//
// Модель не знает их и потому ошибается: обрезает вывод, которого не было,
// просит прочитать файл дважды, ждёт от grep бесконечный результат.
// Машинно-читаемый список лимитов в промпте — дешевле, чем обучение на
// своих ошибках.
var toolLimits = []struct{ tool, limit string }{
	{"read_file", "вернёт не более ~24 000 симв.; у больших файлов читай по частям через offset/limit"},
	{"bash", "вывод обрезан до 16 000 симв.; таймаут 5–600 с (по умолчанию 60); повторять ту же команду с тем же результатом бессмысленно"},
	{"web_fetch", "текст страницы обрезан до 8000 симв. по умолчанию; максимум задаётся max_chars"},
	{"web_search", "до 8 результатов по умолчанию; это HTML-скрапинг DuckDuckGo, поэтому результаты шумные — проверяй факты по первоисточникам"},
	{"grep", "результаты ограничены по числу совпадений; крупный поиск по проекту сначала сузь до каталога"},
	{"todo_write", "план виден только тебе; не переписывай его целиком ради одной отметки"},
	{"agent_status", "agent_status list/status — только сводка; полный отчёт субагента приходит его же результатом"},
	{"ask_user", "в автопилоте вопросы не задаются: вопрос помечается как решённый на твоё усмотрение"},
	{"self_status", "этот инструмент; ничего не меняет"},
	{"extend_turns", "продлевает ход, а не сохраняет состояние: для следующего хода зови handoff; отклоняется при повторах и без обоснования"},
	{"remember", "пишет в долговременную память (~/.gcli/GCLI.md); для заметок внутри задачи — task_note"},
	{"screenshot", "PNG сохраняется в .gcli/screenshots; изображение прикладывается к твоему контексту"},
	{"read_image", "изображение прикладывается к контексту; до 4 картинок за один ход"},
	{"spawn_agent", "пул ограничен; субагент не видит твоего контекста — передай факты в task"},
}

// SelfStatus — текстовый снимок состояния агента для контекста модели.
func (s SelfReport) Text() string {
	var b strings.Builder

	// ---- Контекст: главное, из-за чего чаще всего срывается сессия. ----
	sysTok := core.ApproxTokens(s.SystemPrompt)
	histTok := 0
	for _, m := range s.History {
		histTok += core.ApproxTokens(m.Content) + core.ApproxTokens(m.Reasoning)
		for _, tc := range m.ToolCalls {
			histTok += core.ApproxTokens(tc.Args)
		}
		if len(m.Images) > 0 {
			histTok += 1600 * len(m.Images) // грубая оценка одной картинки
		}
	}
	total := sysTok + histTok

	b.WriteString("# Моё состояние\n\n")

	// Индикатор заполнения контекста — самое полезное, что тут есть:
	// агент видит, сколько ещё можно писать в контекст до сжатия.
	bar, pct := contextBar(total, s.CompactAt)
	if s.CompactAt > 0 {
		fmt.Fprintf(&b, "Контекст: ~%d из %d токенов до сжатия (%d%%), оценка без учёта схем инструментов.\n",
			total, s.CompactAt, pct)
		b.WriteString(bar + "\n")
		if pct > 85 {
			b.WriteString("⚠ Контекст близок к порогу: скоро будет сжатие истории — не тащи новые файлы целиком, читай по частям.\n")
		} else if pct > 60 {
			b.WriteString("Контекст заполнен больше чем наполовину — переходи на точечные чтения вместо чтения файлов целиком.\n")
		}
	} else {
		fmt.Fprintf(&b, "Контекст: ~%d токенов (сжатие отключено).\n", total)
	}
	fmt.Fprintf(&b, "Из них: системный промпт ~%d, история ~%d, сообщений в истории %d.\n",
		sysTok, histTok, len(s.History))

	// ---- Расход и скорость. ----
	if s.Usage.Total() > 0 || s.Stats.Requests > 0 {
		fmt.Fprintf(&b, "\nРасход: %d вход + %d выход = %d токенов", s.Usage.PromptTokens, s.Usage.CompletionTokens, s.Usage.Total())
		if s.Stats.Requests > 0 {
			fmt.Fprintf(&b, ", запросов к модели %d, ошибок %d, среднее %.1f с",
				s.Stats.Requests, s.Stats.Errors, s.Stats.AvgDuration().Seconds())
		}
		if sp := s.Stats.Speed(); sp > 0 {
			fmt.Fprintf(&b, ", скорость %.1f ток/с", sp)
		}
		b.WriteString(".\n")
	}

	// ---- Бюджет итераций: агент видит, что скоро придётся сдавать отчёт. ----
	if s.MaxIters > 0 {
		fmt.Fprintf(&b, "\nИтерации цикла: %d из %d использовано", s.Turns, s.MaxIters)
		left := s.MaxIters - s.Turns
		switch {
		case left <= 2:
			b.WriteString(" — почти всё, пора готовить итог.")
		case left <= 6:
			b.WriteString(" — осталось немного, не распыляйся на «ещё один заход».")
		}
		if s.Extends > 0 {
			fmt.Fprintf(&b, " (продлено %d %s до %d)",
				s.Extends, pluralRU(s.Extends, "раз", "раза", "раз"), s.ExtendAbs)
		} else if s.ExtendAbs > s.MaxIters {
			fmt.Fprintf(&b, ". Продлить можно до %d — инструментом extend_turns с объяснением, зачем.", s.ExtendAbs)
		}
		b.WriteString("\n")
	}

	if len(s.Notes) > 0 {
		fmt.Fprintf(&b, "\nЗаметки по задаче (%d):\n", len(s.Notes))
		for i, n := range s.Notes {
			if i >= 10 {
				fmt.Fprintf(&b, "- …ещё %d\n", len(s.Notes)-10)
				break
			}
			fmt.Fprintf(&b, "- %s\n", core.Truncate(core.OneLine(n), 140))
		}
	}

	if s.ReadFiles > 0 {
		fmt.Fprintf(&b, "\nУже прочитано файлов: %d (они помечены как прочитанные — edit_file по ним разрешён).", s.ReadFiles)
		if s.ReadOnly {
			b.WriteString(" Режим только для чтения: инструменты записи недоступны.")
		}
		b.WriteString("\n")
	}

	if s.Agents != "" {
		fmt.Fprintf(&b, "\nСубагенты:\n%s\n", core.Truncate(s.Agents, 1200))
	}

	// ---- Автономный прогон. ----
	//
	// Отдельный заголовок, а не строка в общий список: Section() режет
	// отчёт по заголовкам, и «сколько раз меня просили продолжить» —
	// ровно тот вопрос, который модель задаёт себе на длинной дистанции
	// чаще всего.
	if s.Mission != "" {
		fmt.Fprintf(&b, "\n# Автономный прогон\n%s\n", s.Mission)
	}

	if s.Subagent != "" {
		fmt.Fprintf(&b, "\nЯ субагент «%s», глубина вложенности %d", s.Subagent, s.Depth)
		if s.Depth >= 1 {
			b.WriteString(" — порождать субагентов нельзя, работай сам.")
		}
		b.WriteString("\n")
	}

	// ---- Лимиты инструментов. ----
	b.WriteString("\n# Лимиты инструментов (чтобы не тратить вызовы впустую)\n")
	for _, l := range toolLimits {
		fmt.Fprintf(&b, "- %s: %s\n", l.tool, l.limit)
	}

	return strings.TrimSpace(b.String())
}

// contextBar — текстовый индикатор заполнения контекста.
func contextBar(total, limit int) (string, int) {
	pct := 0
	if limit > 0 {
		pct = total * 100 / limit
		if pct > 100 {
			pct = 100
		}
	}
	if pct < 0 {
		pct = 0
	}
	filled := pct / 10
	if filled > 10 {
		filled = 10
	}
	bar := "[" + strings.Repeat("█", filled) + strings.Repeat("░", 10-filled) + "]"
	return bar, pct
}

// pluralRU — согласовать существительное с числом по-русски.
func pluralRU(n int, one, few, many string) string {
	if n%100 >= 11 && n%100 <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}

// sectionRe — разделитель блоков в тексте отчёта.
var sectionRe = regexp.MustCompile(`(?m)^# `)

// Section — вернуть только один блок отчёта (по имени раздела). Если раздел
// не найден или имя пустое — вернуть весь отчёт.
func (s SelfReport) Section(name string) string {
	full := s.Text()
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return full
	}
	parts := sectionRe.Split(full, -1)
	heads := sectionRe.FindAllString(full, -1)
	for i, h := range heads {
		if strings.Contains(strings.ToLower(h), name) {
			return strings.TrimSpace(h) + strings.TrimSpace(parts[i+1])
		}
	}
	return full
}

// hSelfStatus — снимок собственного состояния.
func (r *Registry) hSelfStatus(_ context.Context, m map[string]any) (Result, error) {
	if r.env.Self == nil {
		return Result{}, fmt.Errorf("инструмент недоступен в этом окружении")
	}
	rep := r.env.Self()
	txt := rep.Section(ArgStr(m, "section"))
	return Result{
		Text:    txt,
		Summary: fmt.Sprintf("состояние: %d симв.", len([]rune(txt))),
	}, nil
}
