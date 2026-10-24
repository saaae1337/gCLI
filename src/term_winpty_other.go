//go:build !windows

package main

// winptyRelaunch — заглушка для не-Windows: там терминал всегда отдаёт
// настоящий tty, и перезапуск через winpty не нужен. См. term_winpty_windows.go.
func winptyRelaunch(machine bool) (bool, string) { return false, "" }
