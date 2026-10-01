package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gcli/core"
)

// ---------- dry_run ----------
//
// Проверка любого инструмента устроена неудобно: пишешь временный тест или
// скрипт, запускаешь, смотришь вывод, удаляешь. Три-четыре вызова и мусор в
// проекте на всё время проверки. Для проверки самого verify в этой сессии
// понадобился отдельный временный тест — и он был удалён.
//
// dry_run показывает, что инструмент сделал бы, ничего не делая. Он нужен
// не пользователю, а агенту: перед рискованной правкой, перед откатом, перед
// удалением файлов.

// hDryRun — что инструмент сделал бы.
func (r *Registry) hDryRun(_ context.Context, m map[string]any) (Result, error) {
	tool := strings.TrimSpace(ArgStr(m, "tool"))
	args := ArgStr(m, "args")
	if tool == "" {
		return Result{}, fmt.Errorf("укажи tool — имя инструмента для проверки")
	}
	t := r.Get(tool)
	if t == nil {
		return Result{}, fmt.Errorf("инструмента «%s» нет; есть: %s", tool, strings.Join(r.Names(), ", "))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# dry_run: %s\n\n", tool)
	fmt.Fprintf(&b, "Категория: %s, подтверждение: %s\n",
		t.Category, confirmWord(t.NeedsConfirm))
	if args != "" {
		fmt.Fprintf(&b, "Аргументы: %s\n", core.Truncate(core.OneLine(args), 400))
	}
	b.WriteString("\nЧто произойдёт при реальном вызове:\n")

	// Показываем, что именно будет изменено, — без выполнения.
	switch tool {
	case "write_file", "edit_file", "revert_last":
		b.WriteString(r.writeDryRun(t, args))
	case "multi_edit":
		b.WriteString(r.multiEditDryRun(args))
	case "multi_bash":
		b.WriteString(r.multiBashDryRun(args))
	case "bash", "job", "verify":
		cmd := parseArgString(args, "command")
		if cmd == "" {
			cmd = ArgStr(m, "command")
		}
		// Каталог запуска: без этого нельзя честно сказать, где выполнится
		// команда — самый частый обман в предпросмотре.
		dir := r.workDir
		if wd := parseArgString(args, "workdir"); wd != "" {
			dir = r.resolvePath(wd)
		} else if wd := ArgStr(m, "workdir"); wd != "" {
			dir = r.resolvePath(wd)
		}
		// Показываем и то, что скажет песочница: предпросмотр не должен
		// обещать запуск там, куда команда не дойдёт.
		if abs, err := r.pathArg(dir); err != nil {
			fmt.Fprintf(&b, "- каталог запуска: %s\n- песочница запретит запуск: %v\n", dir, err)
			break
		} else {
			dir = abs
		}
		if cmd == "" {
			b.WriteString("- команда не указана: будет подобрана автоматически (verify) либо нужно указать command\n")
		} else {
			b.WriteString(fmt.Sprintf("- выполнится команда: %s\n", core.Truncate(core.OneLine(cmd), 300)))
		}
		fmt.Fprintf(&b, "- каталог запуска: %s", dir)
		if dir != r.workDir {
			b.WriteString(" (не равен рабочему каталогу)")
		}
		b.WriteString("\n- побочные эффекты: процессы, сеть, изменение кэша сборки\n")
		if r.env.Confirm != nil {
			b.WriteString("- спросит подтверждения пользователя\n")
		}
	case "web_fetch", "web_search":
		b.WriteString("- сетевой запрос, файлы проекта не меняются\n")
		b.WriteString("- риск низкий: ограничение только внешним сайтом\n")
	default:
		fmt.Fprintf(&b, "- категория %s: побочные эффекты зависят от реализации\n", t.Category)
		fmt.Fprintf(&b, "- точный прогон без побочных эффектов не гарантируется\n")
	}

	// Общий совет: как проверить инструмент, не мучая проект.
	if t.Category == "write" {
		b.WriteString("\nПроверить без последствий: скопируй целевой файл во временный каталог " +
			"и повтори правку там, либо сначала changes {detail:true}, чтобы увидеть diff до и после.")
	}
	return Result{Text: strings.TrimSpace(b.String()), Summary: "dry_run: " + tool}, nil
}

func confirmWord(need bool) string {
	if need {
		return "да"
	}
	return "нет"
}

// writeDryRun — что изменится при записи.
//
// Путь разбирается тем же resolvePath, что и у настоящего инструмента: иначе
// предпросмотр показывал бы «файл будет создан» для существующего файла
// (относительный путь отличался от пути процесса), то есть вводил бы в
// заблуждение ровно в тот момент, когда агент хотел перестраховаться.
func (r *Registry) writeDryRun(t *Tool, args string) string {
	am := ParseArgs(args)
	raw := ArgStr(am, "path")
	if raw == "" {
		return "- путь не указан в аргументах: точный результат предсказать нельзя\n"
	}
	path := r.resolvePath(raw)
	rel := core.RelToWD(r.workDir, path)
	if rel == "" {
		rel = path
	}
	var b strings.Builder
	if data, err := readFileSafe(path); err == nil {
		cur := string(data)
		fmt.Fprintf(&b, "- файл существует: %s (%d строк, %s)\n", rel, len(core.SplitLines(cur)), core.HumanSize(len(cur)))
		if t.Def.Name == "edit_file" {
			// edit_file меняет фрагмент, а не содержимое целиком: показываем
			// результат реальной замены, иначе предпросмот врал бы на каждом
			// частичном редактировании.
			old := ArgStr(am, "old_string")
			newS := ArgStr(am, "new_string")
			if old == "" {
				b.WriteString("- не указан old_string: результат правки посчитать нельзя\n")
			} else {
				cnt := strings.Count(cur, old)
				switch {
				case cnt == 0:
					b.WriteString("- ⚠ old_string в файле НЕ найден — реальный вызов вернёт ошибку\n")
				case cnt > 1 && !ArgBool(am, "replace_all"):
					fmt.Fprintf(&b, "- ⚠ вхождений %d: без replace_all реальный вызов вернёт ошибку\n", cnt)
				default:
					updated := strings.Replace(cur, old, newS, 1)
					if ArgBool(am, "replace_all") {
						updated = strings.ReplaceAll(cur, old, newS)
					}
					b.WriteString("\n```diff\n" + unifiedDiff(cur, updated, rel) + "\n```\n")
				}
			}
		} else if next := ArgStr(am, "content"); next != "" {
			fmt.Fprintf(&b, "- после записи станет: %d строк, %s\n", len(core.SplitLines(next)), core.HumanSize(len(next)))
			b.WriteString("\n```diff\n" + unifiedDiff(cur, next, rel) + "\n```\n")
		}
	} else {
		fmt.Fprintf(&b, "- файл будет СОЗДАН: %s\n", rel)
	}
	if t.NeedsConfirm {
		b.WriteString("- показывается diff, спрашивается подтверждение\n")
	}
	return b.String()
}

// parseArgString — достать строковое поле из JSON-аргументов (для dry_run).
func parseArgString(args, key string) string {
	return ArgStr(ParseArgs(args), key)
}

// multiEditDryRun — что изменит пакетная правка.
//
// Показываем по каждому файлу тот же результат замены, что даёт
// writeDryRun для одиночного edit_file: предпросмотр пачки, который
// показывает только «применится 3 правки», бесполезен — именно по нему
// решают, не затрёт ли старый_string чужую работу.
func (r *Registry) multiEditDryRun(args string) string {
	am := ParseArgs(args)
	raw, _ := am["edits"].([]any)
	if len(raw) == 0 {
		return "- не указаны правки (edits: пуст)\n"
	}
	var b strings.Builder
	t := &Tool{Def: ToolDef{Name: "edit_file"}}
	for i, v := range raw {
		em, ok := v.(map[string]any)
		if !ok {
			fmt.Fprintf(&b, "- edits[%d]: не объект\n", i)
			continue
		}
		one, _ := json.Marshal(em)
		fmt.Fprintf(&b, "\nПравка %d:\n%s", i+1, r.writeDryRun(t, string(one)))
	}
	if ArgBool(am, "atomic") {
		b.WriteString("\n- atomic: сначала проверяются все правки, применяются они все сразу; " +
			"при первой ошибке не пишется ничего\n")
	}
	b.WriteString("\n- файлы выполняются параллельно; повтор одного файла в edits будет отклонён\n")
	return b.String()
}

// multiBashDryRun — что выполнит пакетная команда.
func (r *Registry) multiBashDryRun(args string) string {
	am := ParseArgs(args)
	cmds := ArgStrSlice(am, "commands")
	if len(cmds) == 0 {
		if c := ArgStr(am, "command"); c != "" {
			cmds = []string{c}
		}
	}
	if len(cmds) == 0 {
		return "- команды не указаны\n"
	}
	dir := r.workDir
	if wd := ArgStr(am, "workdir"); wd != "" {
		if abs, err := r.pathArg(wd); err != nil {
			return fmt.Sprintf("- каталог запуска запрещён песочницей: %v\n", err)
		} else {
			dir = abs
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "- выполнится %d команд(ы) параллельно:\n", len(cmds))
	for i, c := range cmds {
		mark := "  "
		if IsDangerous(c) {
			mark = "⚠ "
		}
		fmt.Fprintf(&b, "  %s%d. %s\n", mark, i+1, core.Truncate(core.OneLine(c), 200))
	}
	fmt.Fprintf(&b, "- каталог запуска: %s", dir)
	if dir != r.workDir {
		b.WriteString(" (не равен рабочему каталогу)")
	}
	b.WriteString("\n- команды выполняются одновременно и НЕ должны зависеть друг от друга\n")
	if r.env.Confirm != nil {
		b.WriteString("- спросит подтверждения пользователя\n")
	}
	return b.String()
}
