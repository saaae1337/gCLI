package main

// Watch-режим: gcli следит за файлами проекта и сам чинит падения проверки.
//
// Зачем: TDD-петля «сохранил файл → упал тест → починил» повторяется десятки
// раз в день, и каждый шаг — ручной ввод. -watch зацикливает её: меняются
// файлы → запускается проверка → упала → агент чинит → проверка ещё раз.
// Человек смотрит в редактор, а не в терминал.
//
// Границы: агент чинит КОД, а не команду — команда считается эталоном.
// Подряд допустимо ограниченное число починок: если агент не выходит из
// ямы, режим честно останавливается, а не жжёт токены до утра.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gcli/core"
)

// watchExcludes — что не считается изменением проекта. Тот же список
// причин, что у снапшотов: кэши и сборки не исходники.
var watchExcludes = map[string]bool{
	".git": true, ".gcli": true, "node_modules": true, "__pycache__": true,
	".venv": true, "venv": true, "vendor": true, "target": true, "dist": true,
	"builds": true,
}

// maxWatchFiles — потолок отслеживаемых файлов: это страховка от
// секундных обходов на огромных деревьях, а не ограничение проекта.
const maxWatchFiles = 20000

// watchState — снимок файлов: путь → размер+mtime.
type watchState map[string][2]int64

// watchFileMap — обойти каталог и собрать состояние файлов.
func watchFileMap(dir string) watchState {
	st := watchState{}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // не читаемый — не отслеживаемый
		}
		if d.IsDir() {
			if watchExcludes[d.Name()] && p != dir {
				return filepath.SkipDir
			}
			return nil
		}
		if len(st) >= maxWatchFiles {
			return filepath.SkipAll
		}
		if info, err := d.Info(); err == nil {
			st[p] = [2]int64{info.Size(), info.ModTime().UnixNano()}
		}
		return nil
	})
	return st
}

// watchChanged — изменилось ли что-то между двумя снимками.
func watchChanged(old, new watchState) bool {
	if len(old) != len(new) {
		return true
	}
	for p, st := range old {
		if n, ok := new[p]; !ok || n != st {
			return true
		}
	}
	return false
}

// watchRunCommand — запустить проверку: вывод, код выхода, флаг таймаута.
func watchRunCommand(dir, command string, timeout time.Duration) (string, int, bool) {
	shell, flag := "sh", "-c"
	if runtime.GOOS == "windows" {
		shell, flag = "cmd", "/c"
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, flag, command)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	timedOut := false
	if err != nil {
		code = 1
		var ee *exec.ExitError
		if asExitError(err, &ee) {
			code = ee.ExitCode()
		} else if ctx.Err() != nil {
			timedOut = true // не ExitError — убито по таймауту
		}
	}
	return string(out), code, timedOut
}

func asExitError(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*target = ee
	}
	return ok
}

// runWatch — основной цикл режима слежения.
//
// Шаг: снять состояние → если изменилось, запустить проверку → упала —
// отдать агенту на починку (до maxFixes подряд) → снова проверить.
// Изменения, сделанные самим агентом, — часть цикла: после успешной
// проверки они просто не приводят к вызову агента.
func (a *app) runWatch(command string, maxFixes int) {
	if strings.TrimSpace(command) == "" {
		a.ui.Err("укажи команду проверки: gcli -watch \"go test ./...\"")
		return
	}
	a.ui.Info("watch: слежу за " + core.RelToWD(a.workDir, a.workDir) + " · проверка: " + command)
	a.ui.Info("остановка: Ctrl+C · починок подряд не более " + fmt.Sprint(maxFixes))

	prev := watchFileMap(a.workDir)
	fixes := 0
	first := true
	for {
		time.Sleep(900 * time.Millisecond)
		cur := watchFileMap(a.workDir)
		if !first && !watchChanged(prev, cur) {
			continue
		}
		prev = cur
		first = false

		out, code, timedOut := watchRunCommand(a.workDir, command, 10*time.Minute)
		if code == 0 {
			if fixes > 0 {
				fixes = 0 // яма пройдена: счётчик подряд обнуляется
			}
			fmt.Printf("[%s] ok\n", time.Now().Format("15:04:05"))
			continue
		}
		fail := fmt.Sprintf("[%s] FAIL (код %d%s)\n%s",
			time.Now().Format("15:04:05"), code,
			map[bool]string{true: ", таймаут", false: ""}[timedOut],
			tailChars(out, 6000))
		fmt.Println(fail)

		fixes++
		if fixes > maxFixes {
			a.ui.Err("watch: подряд " + fmt.Sprint(maxFixes) + " починок не помогли — останавливаюсь, смотри вывод выше")
			return
		}

		prompt := "Watch: проверка проекта упала. Команда: " + command + "\n" +
			"Почини код проекта так, чтобы эта команда снова проходила. Правь код, а не команду: команда — эталон.\n" +
			"Вывод команды:\n```\n" + tailChars(out, 6000) + "\n```"
		if err := a.turn(prompt); err != nil {
			a.ui.Err("watch: ход агента не удался: " + err.Error())
			return
		}
		// После хода агент мог изменить файлы: переснимаем состояние,
		// чтобы не проверять дважды.
		prev = watchFileMap(a.workDir)
	}
}

// tailChars — последний кусок строки по символам: падения тестов
// объясняются в конце вывода, а не в начале.
func tailChars(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}
