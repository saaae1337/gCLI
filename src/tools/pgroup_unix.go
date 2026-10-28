//go:build !windows

package tools

import (
	"os"
	"os/exec"
	"syscall"
)

// PGroup — уводить потомка в собственную группу процессов.
//
// Без Setpgid контекстный таймаут убивает только оболочку: фоновые задачи
// («sleep 60 &»), унаследовавшие stdout/stderr, остаются жить и держат
// вывод открытым. Собственная группа позволяет KillTree добить всё дерево
// одним сигналом.
func PGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// KillTree — убить дерево процессов: группу потомка целиком, а не только
// оболочку. Потомки наследуют группу даже после смерти родителя, поэтому
// отрицательный pid достаёт и осиротевшие фоновые задачи. Если группа уже
// рассыпалась, kill вернёт ESRCH — это не ошибка, прямой потомок добивается
// штатным контекстным Kill.
func KillTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
