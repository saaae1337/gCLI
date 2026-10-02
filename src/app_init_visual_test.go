package main

import (
	"testing"
)

// TestShowInitOutput — временная проверка глазами на демо-проекте.
func TestShowInitOutput(t *testing.T) {
	a, buf, work := initApp(t, map[string]string{
		"go.mod":  "module example.com/demo\n\ngo 1.22\n",
		"main.go": "package main\n\nfunc main() {}\n",
	})
	_ = work
	a.cmdInit("show")
	t.Log("\n" + out(buf))
}
