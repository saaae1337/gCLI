//go:build windows

package main

// Перезапуск через winpty для mintty/Git Bash.
//
// Проблема: mintty (терминал Git Bash) не даёт нативным Windows-программам
// консольные хэндлы — stdin/stdout приходят пайпами. GetConsoleMode на
// таком stdin отвечает отказом, консольного raw-режима нет, и полноэкранный
// интерфейс в Git Bash недостижим в принципе — что бы мы ни делали с
// флагами консоли. Так устроен mintty, а не gcli.
//
// Решение — то же, что у fzf: перезапустить себя под winpty. Он создаёт
// скрытую консоль, садится между терминалом и программой и переводит
// ввод-вывод; у ребёнка stdin снова консоль, и TUI работает. winpty входит
// в Git for Windows (usr/bin/winpty.exe), то есть в окружении, где проблема
// существует, он обычно уже есть.
//
// Предохранители:
//   - GCLI_WINPTY=0 отключает перезапуск совсем;
//   - GCLI_WINPTY_ACTIVE=1 ставится дочернему процессу, иначе цикл
//     перезапусков (если у ребёнка консоль всё равно не получилась);
//   - только интерактивный запуск: -p/-json/-setup/-stats/-bench/-watch/
//     -plain/-ascii и GCLI_TUI=0 идут мимо winpty.

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// winptyRelaunch — перезапустить себя через winpty, если терминал mintty.
//
// machine — машинный режим (-json): в нём перезапуск не нужен никогда.
// Возвращает (true, "") после успешного запуска наследника (вызывающий
// обязан выйти), либо (false, note), где note — необязательное замечание
// для startupNotes: почему перезапуск не состоялся, хотя мог бы.
func winptyRelaunch(machine bool) (bool, string) {
	if runtime.GOOS != "windows" {
		return false, ""
	}
	if os.Getenv("GCLI_WINPTY_ACTIVE") != "" || os.Getenv("GCLI_WINPTY") == "0" {
		return false, ""
	}
	if !looksLikeMintty() {
		return false, ""
	}
	if stdinIsTerminal() {
		// Консоль есть — winpty не нужен (обычный cmd/Windows Terminal).
		return false, ""
	}
	if !interactiveIntent(machine) {
		return false, ""
	}
	winpty, err := exec.LookPath("winpty")
	if err != nil {
		return false, "Git Bash/mintty: консольный ввод недоступен, а winpty не найден — " +
			"полноэкранный интерфейс здесь не поднимется (запусти в Windows Terminal или cmd)"
	}
	exe, err := os.Executable()
	if err != nil {
		return false, ""
	}
	// -Xallow-non-tty: у НАС stdin — пайп из mintty, и без этого флага
	// winpty отказывается стартовать ровно в нашем случае.
	args := append([]string{"-Xallow-non-tty", exe}, os.Args[1:]...)
	cmd := exec.Command(winpty, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "GCLI_WINPTY_ACTIVE=1")
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		return false, fmt.Sprintf("winpty не смог запустить gcli (%v) — продолжаю в построчном режиме", err)
	}
	// Наследник отработал (exit 0) — выходим с его кодом.
	os.Exit(0)
	return true, ""
}

// looksLikeMintty — окружение похоже на mintty/Git Bash.
//
// MSYSTEM ставит Git Bash (MINGW64 и т.п.), TERM_PROGRAM=mintty — сам
// mintty. Windows Terminal и cmd этих переменных не выставляют.
func looksLikeMintty() bool {
	return os.Getenv("MSYSTEM") != "" ||
		strings.EqualFold(os.Getenv("TERM_PROGRAM"), "mintty")
}

// interactiveIntent — запуск похож на интерактивную сессию, а не на
// машинный режим: только в нём полноэкранный интерфейс вообще мог бы быть.
// tuiBlocked уже посчитан в main из флагов (-p, -setup, -stats, -bench,
// -watch, -mcp-serve, -plain); здесь добавляются -json и переменные.
func interactiveIntent(machine bool) bool {
	if machine || tuiBlocked {
		return false
	}
	if os.Getenv("GCLI_TUI") == "0" || os.Getenv("GCLI_ASCII") != "" {
		return false
	}
	return true
}
