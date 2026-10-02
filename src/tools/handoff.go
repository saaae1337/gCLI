package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gcli/core"
)

// ---------- handoff ----------
//
// Лимит итераций упирается в предсказуемо. Агент знает, сколько шагов
// осталось, и всё равно оказывается в ситуации «начал чинить, кончился ход».
// Дальше происходит худшее: следующий ход начинается с нуля, не зная, что уже
// сделано, какой тест красный и на чём остановились. Работа либо делается
// дважды, либо не доводится до конца вообще.
//
// handoff решает это машинно: снимок состояния, который переживает сжатие
// контекста и читается в начале следующего хода. Не проза «чем я занимался»,
// а факты: что изменено, что проверено, что красное, что дальше по плану.
//
// Вызывать надо самому, когда итерации кончаются, — не полагаясь на то, что
// хост умеет остановиться вовремя.

// Handoff — снимок состояния для следующего хода.
type Handoff struct {
	SessionID string
	WorkDir   string
	CreatedAt time.Time
	// Что сделано — по журналу правок.
	Changed []string
	Done    []string
	// Что осталось.
	Pending  []TodoItem
	Blocked  string
	NextStep string
	// Что проверено и с каким результатом.
	Verified string
	// Проект и проверка — чтобы следующий ход не начинал с нуля.
	Root      string
	VerifyCmd string
}

// Text — текст снимка для контекста модели.
func (h Handoff) Text() string {
	var b strings.Builder
	b.WriteString("# Снимок состояния (handoff)\n\n")
	fmt.Fprintf(&b, "Сделан в %s, рабочий каталог: %s.\n", h.CreatedAt.Format("15:04:05"), h.WorkDir)

	if len(h.Changed) > 0 {
		fmt.Fprintf(&b, "\n## Что изменено (%d файлов)\n", len(h.Changed))
		for _, f := range h.Changed {
			b.WriteString("- " + f + "\n")
		}
	} else {
		b.WriteString("\n## Что изменено\nНичего: правок в этом ходе не было.\n")
	}
	if len(h.Done) > 0 {
		b.WriteString("\n## Что сделано\n")
		for _, d := range h.Done {
			b.WriteString("- " + core.Truncate(core.OneLine(d), 200) + "\n")
		}
	}

	// Что осталось — самая ценная часть: именно её агент теряет при сжатии.
	if len(h.Pending) > 0 {
		b.WriteString("\n## Что осталось сделать\n")
		for i, t := range h.Pending {
			mark := "☐"
			switch t.Status {
			case core.TodoInProgress:
				mark = "▸"
			case core.TodoCompleted:
				mark = "✔"
			}
			fmt.Fprintf(&b, "%s %d. %s\n", mark, i+1, t.Content)
		}
	}
	if h.NextStep != "" {
		fmt.Fprintf(&b, "\n## С чего начать следующий ход\n%s\n", core.Truncate(h.NextStep, 600))
	}
	if h.Verified != "" {
		fmt.Fprintf(&b, "\n## Проверка\n%s\n", core.Truncate(h.Verified, 800))
	}
	if h.Blocked != "" {
		fmt.Fprintf(&b, "\n## Затык\n%s\n", core.Truncate(h.Blocked, 400))
	}
	if h.Root != "" {
		b.WriteString("\n## Ориентиры\n")
		fmt.Fprintf(&b, "- корень проекта: %s\n", h.Root)
		if h.VerifyCmd != "" {
			fmt.Fprintf(&b, "- команда проверки: %s\n", h.VerifyCmd)
		}
	}
	b.WriteString("\nПродолжай с этого места: не начинай заново и не перепроверяй уже проверенное.")
	return strings.TrimSpace(b.String())
}

// hHandoff — сохранить снимок состояния.
func (r *Registry) hHandoff(_ context.Context, m map[string]any) (Result, error) {
	h := r.snapshot(strings.TrimSpace(ArgStr(m, "next")), strings.TrimSpace(ArgStr(m, "blocked")), ArgStrSlice(m, "done"))
	path, err := r.saveHandoff(h)
	if err != nil {
		return Result{}, err
	}
	// Возвращаем не только текст: модели должна увидеть его сейчас,
	// а не после следующего хода — иначе смысл инструмента теряется.
	return Result{
		Text:    h.Text() + fmt.Sprintf("\n\n[Снимок сохранён: %s]", core.RelToWD(r.workDir, path)),
		Summary: fmt.Sprintf("handoff: файлов %d, осталось %d", len(h.Changed), len(h.Pending)),
	}, nil
}

// AutoHandoff — снимок без участия модели.
//
// Лимит итераций обрывает ход на полуслове, и следующий ход начинается
// с нуля: потеряно всё, кроме текста отчёта. Снимок собирается
// внутри цикла, когда итерации кончились, — левый в агенте об нем
// не знать, положиться на него нельзя. Факты (файлы, todo,
// результат проверки) берутся из живого состояния
// реестра, поэтому снимок не зависит от того, успел ли агент о нём подумать.
func (r *Registry) AutoHandoff(reason string) (string, error) {
	h := r.snapshot("", "", nil)
	if reason != "" {
		h.Blocked = reason
	}
	if _, err := r.saveHandoff(h); err != nil {
		return "", err
	}
	return h.Text(), nil
}

// snapshot — собрать снимок из живого состояния реестра.
func (r *Registry) snapshot(next, blocked string, done []string) Handoff {
	h := Handoff{
		WorkDir:   r.workDir,
		CreatedAt: time.Now(),
		NextStep:  next,
		Blocked:   blocked,
	}
	for _, d := range done {
		if s := strings.TrimSpace(d); s != "" {
			h.Done = append(h.Done, s)
		}
	}
	// Факты берём из живого состояния, а не просим агента
	// описать их словами: список файлов из журнала точнее
	// любого пересказа.
	seen := map[string]bool{}
	for _, c := range r.change().all() {
		if !seen[c.Path] {
			seen[c.Path] = true
			h.Changed = append(h.Changed, fmt.Sprintf("%s (%s)", c.Rel, c.Label))
		}
	}
	if r.env.Session != nil {
		h.Pending = r.env.Session.Todos()
	}
	info := r.projectInfo()
	h.Root = info.Root
	if cands := DetectVerify(r.workDir); len(cands) > 0 {
		h.VerifyCmd = cands[0].Cmd
	}

	// Последняя проверка — чтобы следующий ход знал, что было
	// красным. Сами названия падений без повода — следующий ход
	// именно вообще повторят каждый раз.
	if base := loadBaseline(baselinePath(r.workDir)); base != nil {
		if base.Failed > 0 {
			h.Verified = fmt.Sprintf("последний прогон: упало %d (команда %s)", base.Failed, base.Command)
			for _, f := range base.Failures {
				h.Verified += "\n  - " + core.Truncate(core.OneLine(f.Where+" — "+f.What), 200)
			}
		} else {
			h.Verified = fmt.Sprintf("последний прогон: зелёный (команда %s)", base.Command)
		}
	}
	return h
}

// saveHandoff — записать снимок на диск.
//
// Снимок должен пережить сжатие контекста и
// следующий ход: в памяти агента нет в памяти.
func (r *Registry) saveHandoff(h Handoff) (string, error) {
	path := handoffPath(r.workDir)
	if err := core.WriteAtomic(path, []byte(h.Text()), 0o644); err != nil {
		return "", fmt.Errorf("не удалось сохранить снимок: %v", err)
	}
	return path, nil
}

// handoffPath — файл снимка, привязанный к проекту.
func handoffPath(workDir string) string {
	return filepath.Join(core.Home(), "handoff", slugOf(workDir)+".md")
}

func slugOf(dir string) string {
	return core.Slug(filepath.Base(dir), 24) + "-" + sha256Hex(filepath.Clean(dir))[:8]
}

// hHandoffRead — прочитать последний снимок (начало нового хода).
func (r *Registry) hHandoffRead(_ context.Context, _ map[string]any) (Result, error) {
	path := handoffPath(r.workDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{
			Text:    "Снимка состояния по этому проекту нет. Значит, предыдущий ход не дошёл до handoff — начни с обследования (project_info), а не с правок вслепую.",
			Summary: "снимка нет",
		}, nil
	}
	return Result{
		Text:    string(data),
		Summary: fmt.Sprintf("снимок: %d симв.", len(data)),
	}, nil
}
