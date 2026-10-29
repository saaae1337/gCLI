//go:build windows

package tools

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

// PGroup на Windows — заглушка: групп процессов нет.
func PGroup(cmd *exec.Cmd) {}

// KillTree — добить дерево процессов.
//
// TerminateProcess не умеет деревья: убитый по таймауту Git Bash оставлял
// живых потомков (ping, sleep, node), а сирота держит текущий каталог и
// открытые файлы — на Windows такой каталог нельзя удалить, и уборка
// TempDir в тестах падала уже после зелёного результата. Теперь потомки
// собираются по снимку Toolhelp32 и terminating'ся до прямого процесса.
//
// Порядок важен: снапшот делается, пока корень жив, — его PID не может
// переиспользоваться, потому что cmd держит хэндл процесса до Wait. У
// каждого добитого потомка дожидаемся смерти: TerminateProcess асинхронен
// ровно до закрытия хэндлов, и без ожидания гонка с RemoveAll
// воспроизводится время от времени.
//
// Полноценная альтернатива — Job Objects (KILL_ON_JOB_CLOSE), но они
// требуют CREATE_SUSPENDED + ResumeThread между Start и Wait, то есть
// обвязки вокруг каждого запуска. Снапшотный добор точечно заменяет
// «убить только прямой процесс» и не трогает ни один вызов.

var (
	modkernel32 = syscall.NewLazyDLL("kernel32.dll")

	procCreateToolhelp32Snapshot = modkernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW          = modkernel32.NewProc("Process32FirstW")
	procProcess32NextW           = modkernel32.NewProc("Process32NextW")
	procOpenProcess              = modkernel32.NewProc("OpenProcess")
	procTerminateProcess         = modkernel32.NewProc("TerminateProcess")
	procWaitForSingleObject      = modkernel32.NewProc("WaitForSingleObject")
	procCloseHandle              = modkernel32.NewProc("CloseHandle")
)

const (
	toolhelpSnapProcess = 0x00000002
	processTerminate    = 0x0001
	waitObject0         = 0x00000000
	waitTimeoutMs       = 2000
)

// invalidHandleValue — INVALID_HANDLE_VALUE как uintptr: CreateToolhelp32Snapshot
// возвращает именно его при ошибке.
const invalidHandleValue = ^uintptr(0)

// processEntry32W — PROCESSENTRY32W из Toolhelp32. Поле uintptr
// (DefaultHeapID) выравнивает структуру до C-раскладки на 64 битах.
type processEntry32W struct {
	Size            uint32
	Usage           uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	Threads         uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [260]uint16
}

// snapshotParentMap — pid → parentPid для всех процессов системы.
// Возвращает nil, если снимок снять не удалось (права, выгрузка системы):
// KillTree в этом случае честно убивает хотя бы прямой процесс.
func snapshotParentMap() map[uint32]uint32 {
	h, _, _ := procCreateToolhelp32Snapshot.Call(toolhelpSnapProcess, 0)
	if h == invalidHandleValue || h == 0 {
		return nil
	}
	defer procCloseHandle.Call(h)

	m := make(map[uint32]uint32)
	var pe processEntry32W
	pe.Size = uint32(unsafe.Sizeof(pe))
	r1, _, _ := procProcess32FirstW.Call(h, uintptr(unsafe.Pointer(&pe)))
	for r1 != 0 {
		m[pe.ProcessID] = pe.ParentProcessID
		pe.Size = uint32(unsafe.Sizeof(pe))
		r1, _, _ = procProcess32NextW.Call(h, uintptr(unsafe.Pointer(&pe)))
	}
	return m
}

// descendants — все потомки root транзитивно (дети, внуки, правнуки).
func descendants(root uint32) []uint32 {
	m := snapshotParentMap()
	if m == nil {
		return nil
	}
	children := make(map[uint32][]uint32)
	for pid, ppid := range m {
		children[ppid] = append(children[ppid], pid)
	}
	var out []uint32
	seen := map[uint32]bool{root: true}
	queue := []uint32{root}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, c := range children[p] {
			if seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, c)
			queue = append(queue, c)
		}
	}
	return out
}

// killPIDWait — TerminateProcess + дождаться смерти процесса. Хэндл
// закрывается только после сигнала «процесс завершён»: к моменту возврата
// ядро уже закрыло его файлы и каталог, и уборка TempDir не споткнётся.
func killPIDWait(pid uint32) {
	h, _, _ := procOpenProcess.Call(processTerminate, 0, uintptr(pid))
	if h == 0 {
		return
	}
	defer procCloseHandle.Call(h)
	procTerminateProcess.Call(h, 1)
	procWaitForSingleObject.Call(h, waitTimeoutMs)
}

// KillTree — убить всё дерево: сначала потомков (пока корень жив и его
// потомки видны в снапшоте), затем сам процесс. Ошибка — только о
// прямом процессе: у потомков своих ошибок не бывает полезных, все
// они уже дожаты или умерли сами.
func KillTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	for _, pid := range descendants(uint32(cmd.Process.Pid)) {
		killPIDWait(pid)
	}
	return cmd.Process.Kill()
}
