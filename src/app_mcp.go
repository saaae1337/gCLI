package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gcli/tools"
)

// ---------- Команда /mcp ----------

// cmdMcp — управление MCP-серверами:
//
//	/mcp            — список серверов и их инструментов;
//	/mcp reload     — переподключиться и дозарегистрировать инструменты;
//	/mcp new        — создать шаблон .gcli/mcp.json;
//	/mcp path       — показать пути конфигов.
func (a *app) cmdMcp(rest string) {
	parts0 := strings.Fields(rest)
	sub := ""
	if len(parts0) > 0 {
		sub = strings.ToLower(parts0[0])
	}
	switch sub {
	case "new", "template", "шаблон":
		path := filepath.Join(a.workDir, ".gcli", "mcp.json")
		if fileExists(path) {
			a.ui.Warn("файл уже существует: " + path)
			return
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			a.ui.Err(err.Error())
			return
		}
		if err := os.WriteFile(path, []byte(tools.MCPTemplate), 0o644); err != nil {
			a.ui.Err(err.Error())
			return
		}
		a.ui.Ok("шаблон создан: " + path)
		a.ui.Hint("отредактируй серверы и выполни /mcp reload")
		return
	case "path", "путь":
		a.ui.Println("")
		for _, d := range a.tools.MCPDirs() {
			mark := "—"
			if fileExists(d[0]) {
				mark = "✓"
			}
			a.ui.KV(mark+" "+d[1], d[0])
		}
		a.ui.Println("")
		return
	case "trust", "доверять":
		// trustCode ждёт parts[1] = имя, поэтому сюда попадает только хвост
		// после «trust». Раньше сюда шли все слова rest, и parts[1]
		// оказывался словом «trust» — /mcp trust <имя> искал сервер по
		// имени «trust» и всегда падал в «не найден».
		parts := []string{"trust"}
		if len(parts0) > 1 {
			parts = append(parts, parts0[1:]...)
		}
		a.trustCode("mcp", parts)
	case "reload", "перезагрузить":
		n, warns := a.tools.MCPReload()
		for _, w := range warns {
			a.ui.Warn(w)
		}
		if n > 0 {
			a.ui.Ok(fmt.Sprintf("MCP: подключено новых инструментов — %d", n))
		} else if len(warns) == 0 {
			a.ui.Info("новых инструментов нет (серверы уже подключены или конфиг пуст)")
		}
		return
	}

	// Список серверов.
	servers, toolCount := a.tools.MCPStatus()
	a.ui.Println("")
	if len(servers) == 0 {
		a.ui.Info("MCP-серверы не настроены")
		a.ui.Hint("создай конфиг: /mcp new — затем /mcp reload")
		a.ui.Hint("формат: .gcli/mcp.json — {\"servers\": {\"имя\": {\"command\": \"...\", \"args\": [...]}}}")
		a.ui.Println("")
		return
	}
	a.ui.Section("MCP-серверы")
	for _, s := range servers {
		a.ui.Println("  " + a.ui.Accent("◆") + " " + s)
	}
	if toolCount > 0 {
		a.ui.Hint(fmt.Sprintf("инструментов в реестре: %d (имена: mcp__сервер__инструмент)", toolCount))
	}
	a.ui.Hint("обновить подключения: /mcp reload")
	a.ui.Println("")
}
