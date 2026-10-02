package main

// Веб-оболочка gcli (-ui) и история сессии по HTTP (/v1/history).
//
// Зачем: OpenCode показал, что терминал — не единственный клиент агента:
// у него TUI, IDE и десктоп-приложение сидят на одном сервере. У gcli
// сервер уже есть (-serve), здесь к нему прикладывается первый
// полноценный графический клиент: та же сессия, тот же ход, те же миссии,
// но в браузере или в десктоп-обёртке (см. desktop/).
//
// Принципы:
//
//      - оболочка встраивается в бинарник (go:embed) — gcli самодостаточен;
//      - страница /ui/ отдаётся без токена: в ней нет данных. Все данные —
//        под /v1/* и по-прежнему закрыты токеном; оболочка спрашивает токен
//        у пользователя и держит его в localStorage;
//      - никакого сборочного конвейера: ванильный HTML+CSS+JS, работают
//        десять лет спустя.

import (
	"embed"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"gcli/core"
)

//go:embed web/dist
var webDist embed.FS

// webRoot — каталог оболочки как файловая система для http.FileServer.
func webRoot() fs.FS {
	sub, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		panic("gcli: встроенный веб-интерфейс не найден: " + err.Error())
	}
	return sub
}

// handleUIRedirect — /ui без слэша: редирект на /ui/, чтобы относительные
// ссылки оболочки (style.css, app.js) резолвились правильно. Токен из
// запроса переносим: после редиректа оболочка подхватит его из URL.
func (s *serveServer) handleUIRedirect(w http.ResponseWriter, r *http.Request) {
	target := "/ui/"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusTemporaryRedirect)
}

// handleHistory — переписка сессии для оболочки: текущая по умолчанию,
// ?session=<id> — любая из списка (/v1/sessions). Служебные сообщения
// (Hidden) не отдаём: их видел только агент.
func (s *serveServer) handleHistory(w http.ResponseWriter, r *http.Request) {
	var msgsSrc []core.Message
	var id, title string
	var agentMode bool
	if id = r.URL.Query().Get("session"); id != "" {
		if !validSessionID(id) {
			jsonWrite(w, http.StatusBadRequest, map[string]string{"error": "плохой id сессии"})
			return
		}
		loaded, err := s.a.repo.LoadSession(id)
		if err != nil {
			jsonWrite(w, http.StatusNotFound, map[string]string{"error": "сессия не найдена: " + id})
			return
		}
		msgsSrc, id, title, agentMode = loaded.Messages, loaded.ID, loaded.Title, loaded.AgentMode
	} else {
		// Снимок под sessMu: ход параллельно добавляет сообщения,
		// и чтение слайса без блокировки — гонка с append.
		sessMu.Lock()
		msgsSrc = append([]core.Message(nil), s.a.sess.Messages...)
		id, title, agentMode = s.a.sess.ID, s.a.sess.Title, s.a.sess.AgentMode
		sessMu.Unlock()
	}
	msgs := make([]map[string]any, 0, len(msgsSrc))
	for _, m := range msgsSrc {
		if m.Hidden {
			continue
		}
		entry := map[string]any{"role": string(m.Role)}
		if m.Sub != "" {
			entry["sub"] = m.Sub
		}
		switch m.Role {
		case core.RoleAssistant:
			entry["content"] = m.Content
			entry["reasoning"] = m.Reasoning
			// Размышления могли быть живым потоком, а история хранит
			// финальный текст: оболочка показывает их свёрнутым блоком.
			tcs := make([]map[string]string, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				tcs = append(tcs, map[string]string{"id": tc.ID, "name": tc.Name, "args": tc.Args})
			}
			if len(tcs) > 0 {
				entry["tool_calls"] = tcs
			}
		case core.RoleUser:
			entry["content"] = m.Content
		case core.RoleTool:
			// Результат инструмента в оболочке живёт под карточкой вызова:
			// достаточно короткой выжимки, полный текст — в TUI.
			entry["name"] = m.Name
			entry["content"] = core.Truncate(core.OneLine(m.Content), 300)
		}
		msgs = append(msgs, entry)
	}
	jsonWrite(w, http.StatusOK, map[string]any{
		"id": id, "title": title, "agent_mode": agentMode,
		"messages": msgs,
	})
}

// validSessionID — id сессии от сети: только безопасные символы.
//
// filepath.Join чистит «..», но id с разделителями и без расширения .json
// мог бы дотянуться до соседних файлов; проще отказать всё, что не похоже
// на имя файла сессии.
func validSessionID(id string) bool {
	if id == "" || len(id) > 64 || strings.Contains(id, "..") ||
		strings.ContainsAny(id, `/\:`) || strings.HasPrefix(id, ".") {
		return false
	}
	for _, r := range id {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
		if !ok {
			return false
		}
	}
	return true
}

// runUI — поднять сервер и открыть оболочку в браузере.
//
// От -serve отличается только добавленным шагом «открыть браузер» и
// токеном прямо в ссылке: локально это удобно, а слушать по умолчанию
// всё равно 127.0.0.1. Кто хочет наружу — задаёт адрес сам и понимает,
// что токен в ссылке тогда лучше не светить: для этого честнее -serve.
func (a *app) runUI(addr, token string) error {
	if token == "" {
		token = serveToken()
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	s := &serveServer{a: a, token: token, bus: a.serveBus}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/ui/?token=%s", ln.Addr().String(), token)
	if !a.quiet {
		a.ui.Title("gcli ui")
		a.ui.KVPairs([][2]string{
			{"адрес", url},
			{"модель", a.model},
		})
		a.ui.Info("оболочка: /ui/ · данные: /v1/* · тот же движок, что в терминале")
		if strings.HasPrefix(ln.Addr().String(), "0.0.0.0") || strings.HasPrefix(ln.Addr().String(), "[::]") {
			a.ui.Warn("сервер слушает все интерфейсы — токен в ссылке виден в логах")
		}
	} else {
		fmt.Printf("gcli ui on %s\n", url)
	}
	openBrowser(url)
	srv := &http.Server{Handler: s.withAuth(s.mux()), ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(ln)
}

// openBrowser — открыть ссылку в браузере по умолчанию. Ошибка не беда:
// адрес и так напечатан, оболочка доступна из любого браузера руками.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if cmd.Start() != nil {
		return
	}
	go func() { _ = cmd.Wait() }() // не зомби
}
