package main

// gcli serve: HTTP-интерфейс к агенту.
//
// Зачем: TUI — не единственный клиент. Сервер держит сессию и миссию,
// а клиенты могут быть любыми: скрипт, веб-панель прогресса прогона,
// телефон в той же Wi-Fi. У OpenCode архитектура клиент-сервер изначально;
// этот файл — первый такой шлюз у gcli: тот же движок, тот же движок
// миссий, но доступ по HTTP+SSE.
//
// Границы MVP: один одновременный ход (второй запрос получает 409),
// ответ — текст последнего сообщения ассистента, события — через SSE.
// Токен обязателен: если не задан -serve-token, генерируется случайный
// и печатается при старте. По умолчанию слушаем 127.0.0.1: наружу —
// только осознанно.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"gcli/core"
)

// serveBus — шина событий для SSE: подписчики получают то, что агент
// печатал бы в TUI (дельты текста, вызовы инструментов, конец хода).
type serveBus struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func newServeBus() *serveBus {
	return &serveBus{subs: map[chan []byte]struct{}{}}
}

func (b *serveBus) publish(event string, payload any) {
	if b == nil {
		return
	}
	data, err := json.Marshal(map[string]any{"event": event, "data": payload, "ts": time.Now().Unix()})
	if err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- data:
		default: // медленный подписчик пропускает события, а не блокирует агента
		}
	}
}

func (b *serveBus) subscribe() chan []byte {
	ch := make(chan []byte, 256)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *serveBus) unsubscribe(ch chan []byte) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

// serveToken — случайный токен, если человек не задал свой.
func serveToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("gcli-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

type serveServer struct {
	a     *app
	token string
	bus   *serveBus

	mu      sync.Mutex
	running bool // ход в работе: второй /v1/message получает 409
}

// jsonWrite — единый ответ JSON.
func jsonWrite(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *serveServer) auth(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	if h == "Bearer "+s.token {
		return true
	}
	// SSE не умеет заголовки из EventSource: допускаем ?token=.
	return r.URL.Query().Get("token") == s.token
}

func (s *serveServer) mux() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("/v1/status", s.handleStatus)
	m.HandleFunc("/v1/message", s.handleMessage)
	m.HandleFunc("/v1/events", s.handleEvents)
	m.HandleFunc("/v1/mission", s.handleMission)
	m.HandleFunc("/v1/mission/stop", s.handleMissionStop)
	m.HandleFunc("/v1/sessions", s.handleSessions)
	m.HandleFunc("/v1/history", s.handleHistory)
	m.HandleFunc("/ui", s.handleUIRedirect)
	m.Handle("/ui/", http.StripPrefix("/ui/", http.FileServer(http.FS(webRoot()))))
	return m
}

// runServe — поднять HTTP-сервер. Блокирует до ошибки слушателя.
func (a *app) runServe(addr, token string) error {
	if token == "" {
		token = serveToken()
	}
	// Пустой host = только локальная петля: наружу — осознанным выбором
	// адреса вида 0.0.0.0:8642.
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	s := &serveServer{a: a, token: token, bus: a.serveBus}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if !a.quiet {
		a.ui.Title("gcli serve")
		a.ui.KVPairs([][2]string{
			{"адрес", "http://" + ln.Addr().String()},
			{"токен", token},
			{"модель", a.model},
		})
		a.ui.Info("эндпоинты: /v1/status · /v1/message · /v1/events · /v1/mission · /v1/sessions · /v1/history · /ui/")
		if strings.HasPrefix(ln.Addr().String(), "0.0.0.0") || strings.HasPrefix(ln.Addr().String(), "[::]") {
			a.ui.Warn("сервер слушает все интерфейсы — токен обязателен")
		}
	} else {
		fmt.Printf("gcli serve on http://%s token=%s\n", ln.Addr().String(), token)
	}
	srv := &http.Server{Handler: s.withAuth(s.mux()), ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(ln)
}

func (s *serveServer) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Оболочка (/ui*) отдаётся без токена: в ней нет данных — только
		// HTML/CSS/JS, одинаковые для всех. Всё содержимое лежит под /v1/*
		// и закрыто токеном; оболочка спрашивает токен сама и хранит его
		// в localStorage. Так токен не обязателен в ссылке на страницу.
		if !strings.HasPrefix(r.URL.Path, "/ui") && !s.auth(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gcli"`)
			jsonWrite(w, http.StatusUnauthorized, map[string]string{"error": "нужен токен: Authorization: Bearer <token>"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *serveServer) handleStatus(w http.ResponseWriter, _ *http.Request) {
	s.a.mu.Lock()
	running := s.a.running
	s.a.mu.Unlock()
	resp := map[string]any{
		"version":    core.Version,
		"provider":   s.a.prov.ID,
		"model":      s.a.model,
		"agent_mode": s.a.sess.AgentMode,
		"running":    running,
		"work_dir":   s.a.workDir,
		"session":    s.a.sess.ID,
	}
	if tr := s.a.currentMissionTr(); tr != nil {
		resp["mission"] = tr.Status()
	}
	jsonWrite(w, http.StatusOK, resp)
}

func (s *serveServer) handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonWrite(w, http.StatusMethodNotAllowed, map[string]string{"error": "только POST"})
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		jsonWrite(w, http.StatusBadRequest, map[string]string{"error": "ждал JSON {\"text\": \"...\"}"})
		return
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		jsonWrite(w, http.StatusConflict, map[string]string{"error": "ход уже выполняется — один одновременный запрос"})
		return
	}
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	s.bus.publish("turn_start", map[string]string{"text": req.Text})
	err := s.a.turn(req.Text)
	// Последний содержательный ответ ассистента — из истории сессии.
	// Снимок под sessMu: ход идёт в этой же функции, но в /v1/status и
	// SSE историю читают параллельно, и живой слайц здесь — гонка.
	msgs := s.a.Messages()
	last := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == core.RoleAssistant && m.Content != "" && len(m.ToolCalls) == 0 {
			last = m.Content
			break
		}
	}
	s.bus.publish("turn_end", map[string]any{"response": last, "error": errString(err)})
	if err != nil {
		jsonWrite(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	jsonWrite(w, http.StatusOK, map[string]string{"response": last})
}

// handleEvents — SSE-поток событий агента.
func (s *serveServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		jsonWrite(w, http.StatusInternalServerError, map[string]string{"error": "SSE не поддержан"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch := s.bus.subscribe()
	defer s.bus.unsubscribe(ch)
	fmt.Fprint(w, "retry: 2000\n\n")
	fl.Flush()
	for {
		select {
		case data := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", data)
			fl.Flush()
		case <-r.Context().Done():
			return
		case <-time.After(15 * time.Second):
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
		}
	}
}

func (s *serveServer) handleMission(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonWrite(w, http.StatusOK, missionPayload(s.a))
	case http.MethodPost:
		var req struct {
			Mode      string `json:"mode"`
			Objective string `json:"objective"`
			Deadline  string `json:"deadline"`
			Budget    string `json:"budget"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonWrite(w, http.StatusBadRequest, map[string]string{"error": "ждал JSON mission"})
			return
		}
		args := strings.TrimSpace(strings.Join([]string{
			req.Mode, req.Deadline, req.Budget, req.Objective}, " "))
		s.a.missionStart(args)
		jsonWrite(w, http.StatusOK, missionPayload(s.a))
	default:
		jsonWrite(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET или POST"})
	}
}

// missionPayload — задание и живое состояние прогона в структурированном
// виде. Раньше GET /v1/mission возвращал строки Summary()/Status(), которые
// оболочка пыталась читать как JSON-объекты: карточка миссии не появлялась
// никогда, кнопка остановки была недостижима.
func missionPayload(a *app) map[string]any {
	m := a.mission
	mission := map[string]any{
		"objective": m.Objective,
		"mode":      string(m.Mode),
		"summary":   m.Summary(),
	}
	status := map[string]any{"state": "idle"}
	if tr := a.currentMissionTr(); tr != nil {
		if tr.Done() {
			status["state"] = "stopped"
		} else {
			status["state"] = "running"
		}
		status["elapsed"] = core.FormatDur(tr.Elapsed())
		status["spent_tokens"] = tr.Spent()
		status["tool_calls"] = tr.ToolCalls()
		status["iters"] = tr.Iters()
		status["stop_reason"] = tr.Stopped()
	}
	mission["status"] = status
	mission["active"] = status["state"] == "running"
	return map[string]any{"mission": mission}
}

func (s *serveServer) handleMissionStop(w http.ResponseWriter, _ *http.Request) {
	s.a.missionStop()
	jsonWrite(w, http.StatusOK, map[string]string{"ok": "true"})
}

func (s *serveServer) handleSessions(w http.ResponseWriter, _ *http.Request) {
	ss := s.a.repo.ListSessions()
	out := make([]map[string]any, 0, len(ss))
	for i, sess := range ss {
		if i >= 30 {
			break
		}
		out = append(out, map[string]any{
			"id": sess.ID, "title": sess.Title, "updated": sess.Updated,
			"messages": len(sess.Messages),
		})
	}
	jsonWrite(w, http.StatusOK, out)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
