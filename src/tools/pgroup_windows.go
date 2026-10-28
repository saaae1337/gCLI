//go:build windows

package tools

import (
	"os"
	"os/exec"
)

// PGroup на Windows — заглушка: групп процессов нет. Полноценное решение —
// Job Objects, и пока в нём не понадобилось.
func PGroup(cmd *exec.Cmd) {}

// KillTree — на Windows добиваем только прямой потомок: TerminateProcess
// не умеет деревья. Потомок, унаследовавший вывод, подрезается WaitDelay
// (см. hBash): вызов не зависает, хотя сирота может дожить до конца.
func KillTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}
