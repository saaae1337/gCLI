package tools

import (
	"context"
	"fmt"
	"strings"

	"gcli/core"
)

// ---------- git_diff ----------
//
// Правку агента можно было посмотреть только через `git diff` в bash.
// Это стоит целый вызов, возвращает шум (git на Windows сыпет
// «warning: LF will be replaced by CRLF» на любую команду с diff), и
// самое главное — тратит подтверждение человека на чтение.
//
// При этом diff — самый частый повод открыть bash после правки: чтобы
// убедиться, что получилось то, что задумано, и ничего лишнего не
// задело. Обычный инструмент отвечает на него без оглядки на
// подтверждения, потому что ничего не меняет.

// gitDiffMaxBytes — потолок вывода. Diff больше этого уже не прочитать
// за один взгляд, а в контекст он попадает целиком.
const gitDiffMaxBytes = 60000

// gitDiffStat — краткая сводка вместо самого diff.
func gitDiffStat(r *Registry) (string, error) {
	root := gitRepoRoot(r.workDir)
	if root == "" {
		return "", fmt.Errorf("каталог не под git — дифф не показать")
	}
	out, err := gitOut(root, "diff", "--stat", "HEAD")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		st, err := gitOut(root, "status", "--porcelain")
		if err == nil && strings.TrimSpace(st) == "" {
			return "Рабочее дерево чистое: незакоммиченных изменений нет.", nil
		}
		return "Незакоммиченных изменений по отслеживаемым файлам нет.\n" + st, nil
	}
	return out, nil
}

// hGitDiff — изменения относительно HEAD или указанного ref.
func (r *Registry) hGitDiff(_ context.Context, m map[string]any) (Result, error) {
	root := gitRepoRoot(r.workDir)
	if root == "" {
		return Result{}, fmt.Errorf("каталог не под git — сравнивать не с чем. " +
			"Другие способы увидеть правку: changes (журнал write_file/edit_file), " +
			"changes с detail (дифф последних правок), revert_last с dry_run (что откатится)")
	}
	if ArgBool(m, "stat") {
		out, err := gitDiffStat(r)
		if err != nil {
			return Result{}, err
		}
		return Result{Text: out, Summary: "diff --stat"}, nil
	}

	// ref — с чем сравниваем. Пусто = HEAD, то есть незакоммиченные
	// правки; имя может прийти от модели, поэтому идёт отдельным
	// аргументом, а не склейкой в строку (см. gitOut).
	ref := strings.TrimSpace(ArgStr(m, "ref"))
	args := []string{"diff", "--no-color"}
	switch {
	case ref != "":
		args = append(args, ref)
	case ArgBool(m, "staged"):
		args = append(args, "--staged")
	default:
		args = append(args, "HEAD")
	}
	// Путь проверяем песочницей: diff по файлу за пределами рабочего
	// каталога — это чтение чужого дерева в обход границы.
	if p := strings.TrimSpace(ArgStr(m, "path")); p != "" {
		abs, err := r.pathArg(p)
		if err != nil {
			return Result{}, err
		}
		args = append(args, "--", core.RelToWD(r.workDir, abs))
	}

	out, err := gitOut(root, args...)
	if err != nil {
		return Result{}, fmt.Errorf("git diff не отработал: %w", err)
	}
	what := "незакоммиченные правки"
	if ref != "" {
		what = "изменения относительно " + ref
	} else if ArgBool(m, "staged") {
		what = "подготовленные к коммиту правки"
	}
	if strings.TrimSpace(out) == "" {
		return Result{Text: "Пусто: " + what + " отсутствуют.", Summary: "diff пуст"}, nil
	}
	cut := false
	if len(out) > gitDiffMaxBytes {
		out = out[:gitDiffMaxBytes]
		cut = true
	}
	tail := ""
	if cut {
		tail = fmt.Sprintf("\n\n[diff обрезан на %s — сузь выборку через path или ref]",
			core.HumanSize(gitDiffMaxBytes))
	}
	return Result{
		Text:    "```diff\n" + out + "\n```" + tail,
		Summary: fmt.Sprintf("diff: %s", what),
	}, nil
}
