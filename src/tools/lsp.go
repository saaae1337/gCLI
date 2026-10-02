package tools

// LSP-интеграция: language servers как источник обратной связи для агента.
//
// Зачем: компилятор видит только то, что собрал, а language server —
// типы и символы прямо в момент правки. Диагностика после каждого
// edit_file/write_file — это «ты сломал тип на строке 42», увиденное
// сразу, без гонок со сборкой. Так же устроен OpenCode: LSP-серверы
// поднимаются автоматически и кормят модель diagnostics as feedback.
//
// Как: серверы запускаются лениво по расширению файла (gopls для .go,
// typescript-language-server для .ts и т.д.), общаются по stdio
// (JSON-RPC с Content-Length-фреймингом) и живут в одном хабе на весь
// процесс — реестры копируются (Restrict/Merge), а серверы должны
// переживать копии. Нет бинарника — инструмент честно сообщает об этом,
// а тихий хук после правки молчит: страховка не должна ломать ход.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gcli/core"
)

// lspPresets — серверы по умолчанию: расширение → команда.
// Переопределяется ключом "lsp" в config.json ({"lsp": {".go": "gopls"}}).
var lspPresets = map[string]string{
	".go":  "gopls",
	".ts":  "typescript-language-server --stdio",
	".tsx": "typescript-language-server --stdio",
	".js":  "typescript-language-server --stdio",
	".jsx": "typescript-language-server --stdio",
	".py":  "pyright-langserver --stdio",
	".rs":  "rust-analyzer",
}

// lspLangID — languageId для didOpen по расширению.
var lspLangID = map[string]string{
	".go": "go", ".ts": "typescript", ".tsx": "typescriptreact",
	".js": "javascript", ".jsx": "javascriptreact",
	".py": "python", ".rs": "rust",
}

// lspSeverity — человекочитаемые уровни диагностики (LSP 1..4).
var lspSeverity = map[float64]string{
	1: "ошибка", 2: "предупреждение", 3: "инфо", 4: "подсказка",
}

// LSPHub — менеджер language servers на весь процесс.
type LSPHub struct {
	// cfgCmds — переопределения из config.json: {".go": "gopls"}.
	cfgCmds map[string]string
	enabled bool

	mu    sync.Mutex
	conns map[string]*lspConn // ключ: workDir + "\x00" + расширение
}

// NewLSPHub — хаб. enabled=false (конфиг lsp_enabled: false) или nil-карта —
// всё равно корректен: инструменты честно ответят «недоступно».
func NewLSPHub(cfgCmds map[string]string, enabled bool) *LSPHub {
	return &LSPHub{cfgCmds: cfgCmds, enabled: enabled, conns: map[string]*lspConn{}}
}

// Shutdown — остановить все серверы. Зовётся один раз перед выходом.
func (h *LSPHub) Shutdown() {
	if h == nil {
		return
	}
	h.mu.Lock()
	conns := h.conns
	h.conns = map[string]*lspConn{}
	h.mu.Unlock()
	for _, c := range conns {
		c.stop()
	}
}

// ServerFor — команда сервера и languageId для файла. ok=false, если
// расширение не покрыто ни пресетами, ни конфигом.
func (h *LSPHub) ServerFor(path string) (args []string, lang string, ok bool) {
	if h == nil || !h.enabled {
		return nil, "", false
	}
	ext := strings.ToLower(filepath.Ext(path))
	cmd, ok := h.cfgCmds[ext]
	if !ok {
		cmd, ok = lspPresets[ext]
	}
	if !ok {
		return nil, "", false
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return nil, "", false
	}
	if _, err := exec.LookPath(fields[0]); err != nil {
		return nil, "", false
	}
	id, ok := lspLangID[ext]
	if !ok {
		id = strings.TrimPrefix(ext, ".")
	}
	return fields, id, true
}

// Supported — настроен ли и доступен ли сервер для файла (для тихого хука).
func (h *LSPHub) Supported(path string) bool {
	args, _, ok := h.ServerFor(path)
	return ok && len(args) > 0
}

// conn — взять или запустить сервер для workDir+ext.
func (h *LSPHub) conn(workDir, path string) (*lspConn, error) {
	args, lang, ok := h.ServerFor(path)
	if !ok {
		ext := strings.ToLower(filepath.Ext(path))
		if h.cfgCmds[ext] != "" || lspPresets[ext] != "" {
			return nil, fmt.Errorf("сервер %q не найден в PATH — установи его или переопредели ключом lsp в config.json",
				firstNonEmpty(h.cfgCmds[ext], lspPresets[ext]))
		}
		return nil, fmt.Errorf("для %s нет language server (пресеты: %s)",
			filepath.Base(path), strings.Join(mapKeysString(lspPresets), " "))
	}
	key := workDir + "\x00" + strings.ToLower(filepath.Ext(path))
	h.mu.Lock()
	if c := h.conns[key]; c != nil && !c.dead() {
		h.mu.Unlock()
		return c, nil
	}
	h.mu.Unlock()
	c, err := startLSPExec(workDir, args, lang)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	// Гонка двух горутин на старте: оставляем первую выжившую.
	if old := h.conns[key]; old != nil && !old.dead() {
		h.mu.Unlock()
		c.stop()
		return old, nil
	}
	h.conns[key] = c
	h.mu.Unlock()
	return c, nil
}

// AutoDiagnostics — тихий хук после правки файла: строка с диагностикой
// или пусто. Любая ошибка = пусто: вызывается изнутри edit_file/write_file,
// и падать из-за страховки нельзя.
func (h *LSPHub) AutoDiagnostics(workDir, path string) string {
	if h == nil || !h.Supported(path) {
		return ""
	}
	c, err := h.conn(workDir, path)
	if err != nil {
		return ""
	}
	return c.diagnosticsAfterEdit(path, 2500*time.Millisecond)
}

// Diagnostics — честная диагностика для инструмента lsp_diagnostics.
func (h *LSPHub) Diagnostics(workDir, path string) (string, error) {
	c, err := h.conn(workDir, path)
	if err != nil {
		return "", err
	}
	return c.diagnosticsAfterEdit(path, 5*time.Second), nil
}

// Hover — что знает сервер о символе в позиции (строка с 1, столбец в рунах с 0).
func (h *LSPHub) Hover(workDir, path string, line, character int) (string, error) {
	c, err := h.conn(workDir, path)
	if err != nil {
		return "", err
	}
	return c.hover(path, line, character)
}

// Definition — куда смотрит символ: «путь:строка:столбец».
func (h *LSPHub) Definition(workDir, path string, line, character int) (string, error) {
	c, err := h.conn(workDir, path)
	if err != nil {
		return "", err
	}
	return c.definition(path, line, character)
}

// ---------- URI ----------

// pathToURI — file://-URI из пути. filepath.ToSlash: на Windows «C:\x»
// обязано стать «file:///C:/x», а не «file://C:\x».
//
// Про слэш перед буквой диска. Без него url.String() печатает
// «file://C:/x», и это НЕ тот же URI, что «file:///C:/x»: разбор
// первого раскладывает его на Host="C:" + Path="/x", и буква диска
// уезжает из пути в узел хоста. Обратное преобразование потом не
// может её вернуть — путь указывает на другой диск. Лишний слэш
// убирается за счёт url.URL, который с Host="" схлопывает «//»
// в один — так что «/home/me/x» остаётся «file:///home/me/x».
func pathToURI(path string) string {
	p := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	return u.String()
}

// uriToPath — обратное преобразование. Ведущий «/» перед «C:/» на Windows
// добавляется правилами URL и пути не соответствует — срезаем.
//
// Про Host. У формы «file://C:/x» (её генерировал старый pathToURI и её
// до сих пор шлют часть серверов) буква диска осела в узле хоста, а путь
// начинается с «/». Чтобы такой URI не превратился в «\x» на текущем
// диске, хост возвращаем обратно в путь. Канонический «file:///C:/x» идёт
// с пустым Host, и эта ветка его не трогает.
func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	if u.Scheme != "file" {
		return uri
	}
	p := u.Path
	if u.Host != "" {
		p = u.Host + p
	}
	if vol := filepath.VolumeName(strings.TrimPrefix(p, "/")); vol != "" {
		p = strings.TrimPrefix(p, "/")
	}
	return filepath.FromSlash(p)
}

// ---------- Позиции: руны ↔ UTF-16 ----------
// LSP меряет столбец в UTF-16-кодовых единицах, модель думает в символах
// из read_file. Пересчёт в обе стороны: эмодзи и знаки за пределами BMP
// занимают 2 единицы UTF-16, но один rune.

func utf16ColOf(line string, runeCol int) int {
	n := 0
	i := 0
	for _, r := range line {
		if i >= runeCol {
			break
		}
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
		i++
	}
	return n
}

func runeColOf(line string, utf16Col int) int {
	n := 0
	i := 0
	for _, r := range line {
		if i >= utf16Col {
			break
		}
		if r > 0xFFFF {
			i += 2
		} else {
			i++
		}
		n++
	}
	return n
}

// lineOf — строка файла по номеру с 0 (без \r: LSP не любит CR).
func lineOf(text string, line int) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if line < 0 || line >= len(lines) {
		return ""
	}
	return lines[line]
}

// ---------- Типы протокола ----------

type lspMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *lspError       `json:"error,omitempty"`
}

type lspError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type lspDiag struct {
	Range struct {
		Start struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"start"`
		End struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"end"`
	} `json:"range"`
	Severity float64 `json:"severity,omitempty"`
	Message  string  `json:"message"`
	Source   string  `json:"source,omitempty"`
}

// lspResponse — разбор ответа метода из reader-горутины.
type lspResponse struct {
	result json.RawMessage
	err    *lspError
}

// lspConn — одно соединение с language server.
//
// Запросы строго по одному не нужны: JSON-RPC позволяет конвейер, ответы
// маршрутизируются по id. Писать в stdout сервера обязан только один
// горутина-писатель — иначе куски JSON перемешаются.
type lspConn struct {
	stdin io.WriteCloser
	cmd   *exec.Cmd

	workDir string

	mu       sync.Mutex
	nextID   int64
	pending  map[int64]chan lspResponse
	diags    map[string][]lspDiag // uri → последние diagnostics
	pushAt   map[string]time.Time // uri → время последнего push
	pushSeq  map[string]uint64    // uri → счётчик push'ей (свежее времени)
	opened   map[string]int64     // uri → версия документа
	closed   bool
	rootURI  string
	language string
}

// startLSPExec — запустить сервер и пройти handshake.
func startLSPExec(workDir string, args []string, lang string) (*lspConn, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = workDir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard // логи сервера не должны попадать в протокол
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("lsp: не удалось запустить %s: %v", args[0], err)
	}
	c := newLSPConn(stdout, stdin, cmd, workDir, lang)
	if err := c.initialize(); err != nil {
		c.stop()
		return nil, err
	}
	return c, nil
}

func newLSPConn(r io.Reader, w io.WriteCloser, cmd *exec.Cmd, workDir, lang string) *lspConn {
	c := &lspConn{
		stdin: w, cmd: cmd,
		workDir:  workDir,
		pending:  map[int64]chan lspResponse{},
		diags:    map[string][]lspDiag{},
		pushAt:   map[string]time.Time{},
		pushSeq:  map[string]uint64{},
		opened:   map[string]int64{},
		rootURI:  pathToURI(workDir),
		language: lang,
	}
	go c.readLoop(bufio.NewReaderSize(r, 1<<20))
	return c
}

func (c *lspConn) dead() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *lspConn) stop() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	pending := c.pending
	c.pending = map[int64]chan lspResponse{}
	c.mu.Unlock()
	// Сначала вежливо: shutdown → exit (протокол LSP). Потом добиваем.
	_, _ = c.call(10*time.Second, "shutdown", nil)
	_ = c.notify("exit", nil)
	_ = c.stdin.Close()
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	for id, ch := range pending {
		select {
		case ch <- lspResponse{err: &lspError{Message: "соединение закрыто"}}:
		default:
		}
		close(ch)
		_ = id
	}
}

// writeFrame — Content-Length-фрейминг LSP: заголовок + тело.
func writeFrame(w io.Writer, body []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

// readFrame — разобрать один фрейм. Возврат false = поток кончился.
func readFrame(r *bufio.Reader) ([]byte, bool) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, false
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // конец заголовков
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			n, err := strconv.Atoi(strings.TrimSpace(strings.SplitN(line, ":", 2)[1]))
			if err == nil {
				length = n
			}
		}
	}
	if length <= 0 {
		return nil, false
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, false
	}
	return body, true
}

func (c *lspConn) readLoop(r *bufio.Reader) {
	for {
		body, ok := readFrame(r)
		if !ok {
			c.mu.Lock()
			c.closed = true
			pending := c.pending
			c.pending = map[int64]chan lspResponse{}
			c.mu.Unlock()
			for id, ch := range pending {
				select {
				case ch <- lspResponse{err: &lspError{Message: "сервер закрыл поток"}}:
				default:
				}
				close(ch)
				delete(pending, id)
			}
			return
		}
		var msg lspMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		if msg.Method == "" {
			c.mu.Lock()
			if ch := c.pending[msg.ID]; ch != nil {
				delete(c.pending, msg.ID)
				ch <- lspResponse{result: msg.Result, err: msg.Error}
				close(ch)
			}
			c.mu.Unlock()
			continue
		}
		switch msg.Method {
		case "textDocument/publishDiagnostics":
			var p struct {
				URI         string    `json:"uri"`
				Diagnostics []lspDiag `json:"diagnostics"`
			}
			if json.Unmarshal(msg.Params, &p) == nil {
				c.mu.Lock()
				c.diags[p.URI] = p.Diagnostics
				c.pushAt[p.URI] = time.Now()
				c.pushSeq[p.URI]++
				c.mu.Unlock()
			}
		default:
			// server→client запросы (workspace/configuration и т.п.) не отвечаем:
			// серверы работают и без ответов, а протокольный шум нам не нужен.
		}
	}
}

func (c *lspConn) write(msg lspMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("lsp: соединение закрыто")
	}
	return writeFrame(c.stdin, body)
}

func (c *lspConn) notify(method string, params any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.write(lspMessage{JSONRPC: "2.0", Method: method, Params: body})
}

// call — запрос-ответ. Ответы маршрутизируются по id: параллельные
// вызовы разрешены, писатель один (внутри write), значит протокол цел.
func (c *lspConn) call(timeout time.Duration, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan lspResponse, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	if err := c.write(lspMessage{JSONRPC: "2.0", ID: id, Method: method, Params: body}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case resp := <-ch:
		if resp.err != nil {
			return nil, fmt.Errorf("lsp %s: %s", method, resp.err.Message)
		}
		return resp.result, nil
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("lsp %s: таймаут %s", method, timeout)
	}
}

// initialize — handshake: initialize → initialized. Без него сервер
// не отвечает на остальные методы.
func (c *lspConn) initialize() error {
	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   c.rootURI,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"publishDiagnostics": map[string]any{"relatedInformation": false},
			},
		},
	}
	if _, err := c.call(30*time.Second, "initialize", params); err != nil {
		return err
	}
	return c.notify("initialized", map[string]any{})
}

// ensureOpen — didOpen или didChange полным текстом. Держим сервер в
// курсе содержимого с диска: агент мог поправить файл между вызовами.
func (c *lspConn) ensureOpen(path, text string) error {
	uri := pathToURI(path)
	c.mu.Lock()
	ver := c.opened[uri] + 1
	c.opened[uri] = ver
	c.mu.Unlock()
	if ver == 1 {
		return c.notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri": uri, "languageId": c.language, "version": ver, "text": text,
			},
		})
	}
	return c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": ver},
		"contentChanges": []map[string]any{{"text": text}},
	})
}

func (c *lspConn) position(path string, line, character int) (uri string, pos map[string]any, text string, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", nil, "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", nil, "", fmt.Errorf("lsp: %v", err)
	}
	text = string(data)
	if line < 1 || character < 0 {
		return "", nil, "", fmt.Errorf("lsp: позиция указывается как строка с 1 и столбец с 0")
	}
	src := lineOf(text, line-1)
	return pathToURI(abs), map[string]any{
		"line": line - 1, "character": utf16ColOf(src, character),
	}, text, nil
}

// diagnosticsAfterEdit — diagnostics для файла после правки.
//
// Схема сбора: сначала ждём push (publishDiagnostics, основной канал),
// с ранним выходом, как только пришло свежее. Если сервер молчит —
// пробуем pull (textDocument/diagnostic, LSP 3.17). Так покрыты оба
// вида серверов.
func (c *lspConn) diagnosticsAfterEdit(path string, wait time.Duration) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	// Свежесть push'а измеряем СЧЁТЧИКОМ, а не временем: сравнение
	// «pushAt позже t0» оказалось ненадёжным. Разница между приходом
	// события и снятием отметки — единицы миллисекунд, а детализация
	// clock на Windows около 15,6 мс: отметка округлялась вниз и
	// оказывалась РАНЬШЕ события, условие не срабатывало, и мы зря ждали
	// весь таймаут, а потом уходили в запасной textDocument/diagnostic.
	// Счётчик событий от такого не зависит.
	//
	// Отметку снимаем ДО отправки didOpen: сервер отвечает push'ом на
	// неё, и событие может прийти между вызовами.
	c.mu.Lock()
	seq0 := c.pushSeq[pathToURI(abs)]
	c.mu.Unlock()

	t0 := time.Now()
	if err := c.ensureOpen(abs, string(data)); err != nil {
		return ""
	}
	uri := pathToURI(abs)
	deadline := t0.Add(wait)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		seq := c.pushSeq[uri]
		c.mu.Unlock()
		if seq > seq0 {
			return formatDiagnostics(abs, string(data), c.diagsOf(uri))
		}
		time.Sleep(120 * time.Millisecond)
	}
	// Push не пришёл — тянем сами. Ошибку глотаем: снаружи решают,
	// тихий это хук или честный вызов инструмента.
	if res, err := c.call(8*time.Second, "textDocument/diagnostic", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	}); err == nil {
		var p struct {
			Items []lspDiag `json:"items"`
		}
		if json.Unmarshal(res, &p) == nil {
			return formatDiagnostics(abs, string(data), p.Items)
		}
	}
	return ""
}

func (c *lspConn) diagsOf(uri string) []lspDiag {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.diags[uri]
}

// formatDiagnostics — текстовый отчёт. Столбец пересчитывается из UTF-16
// сервера в руны по исходному тексту файла: read_file показывает модель
// руны, и «сходи в столбец N» должно совпадать с тем, что она видит.
func formatDiagnostics(path, text string, items []lspDiag) string {
	if len(items) == 0 {
		return "LSP-диагностика: проблем нет (" + filepath.Base(path) + ")"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "LSP-диагностика после правки (%s): %d замечаний", filepath.Base(path), len(items))
	for i, d := range items {
		if i >= 20 {
			fmt.Fprintf(&b, "\n  … и ещё %d — вызови lsp_diagnostics для полного списка", len(items)-i)
			break
		}
		sev := lspSeverity[d.Severity]
		if sev == "" {
			sev = "замечание"
		}
		src := ""
		if d.Source != "" {
			src = " (" + d.Source + ")"
		}
		fmt.Fprintf(&b, "\n  [%s] %d:%d — %s%s",
			sev, d.Range.Start.Line+1, runeColOf(lineOf(text, d.Range.Start.Line), d.Range.Start.Character), d.Message, src)
	}
	return b.String()
}

func (c *lspConn) hover(path string, line, character int) (string, error) {
	uri, pos, text, err := c.position(path, line, character)
	if err != nil {
		return "", err
	}
	if err := c.ensureOpen(filepath.FromSlash(uriToPath(uri)), text); err != nil {
		return "", err
	}
	res, err := c.call(15*time.Second, "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     pos,
	})
	if err != nil {
		return "", err
	}
	if len(res) == 0 || string(res) == "null" {
		return "hover: сервер не знает о символе в этой позиции", nil
	}
	var p struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := json.Unmarshal(res, &p); err != nil {
		return "", fmt.Errorf("lsp hover: неожиданный ответ сервера")
	}
	value := hoverText(p.Contents)
	if strings.TrimSpace(value) == "" {
		return "hover: сервер не знает о символе в этой позиции", nil
	}
	return fmt.Sprintf("hover %s:%d:%d:\n%s", filepath.Base(path), line, character, core.Truncate(value, 2000)), nil
}

// hoverText — достать текст из вариантов contents: строка, MarkupContent,
// [{value}] — серверы выдают все три вида.
func hoverText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var mk struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &mk) == nil {
		return mk.Value
	}
	var list []struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &list) == nil {
		parts := make([]string, 0, len(list))
		for _, v := range list {
			parts = append(parts, v.Value)
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}

func (c *lspConn) definition(path string, line, character int) (string, error) {
	uri, pos, text, err := c.position(path, line, character)
	if err != nil {
		return "", err
	}
	if err := c.ensureOpen(filepath.FromSlash(uriToPath(uri)), text); err != nil {
		return "", err
	}
	res, err := c.call(15*time.Second, "textDocument/definition", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     pos,
	})
	if err != nil {
		return "", err
	}
	if len(res) == 0 || string(res) == "null" {
		return "определение не найдено", nil
	}
	type loc struct {
		URI   string `json:"uri"`
		Range struct {
			Start struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"start"`
		} `json:"range"`
	}
	one := func(l loc) string {
		p := uriToPath(l.URI)
		return fmt.Sprintf("%s:%d:%d", core.RelToWD(c.workDir, p), l.Range.Start.Line+1, l.Range.Start.Character)
	}
	var single loc
	if json.Unmarshal(res, &single) == nil && single.URI != "" {
		return one(single), nil
	}
	var many []loc
	if json.Unmarshal(res, &many) == nil && len(many) > 0 {
		parts := make([]string, 0, len(many))
		for i, l := range many {
			if i >= 10 {
				parts = append(parts, fmt.Sprintf("… и ещё %d", len(many)-i))
				break
			}
			parts = append(parts, one(l))
		}
		return "определения:\n  " + strings.Join(parts, "\n  "), nil
	}
	return "определение не найдено", nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func mapKeysString(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ { // сортировка вставками: карт мало
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// ---------- Инструменты ----------

const schemaLSPDiag = `{"type":"object","properties":{"path":{"type":"string","description":"путь до файла"}},"required":["path"]}`

const schemaLSPPos = `{"type":"object","properties":{"path":{"type":"string","description":"путь до файла"},"line":{"type":"integer","description":"строка, начиная с 1"},"character":{"type":"integer","description":"столбец в символах, начиная с 0"}},"required":["path","line","character"]}`

func (r *Registry) lspTarget(m map[string]any) (string, error) {
	raw := ArgStr(m, "path")
	if raw == "" {
		return "", fmt.Errorf("укажи path")
	}
	return r.pathArg(raw)
}

func lspUnavailable() error {
	return fmt.Errorf("LSP недоступен: нет подходящего language server в PATH; установи gopls/pyright/tsserver или задай ключ lsp в config.json")
}

// hLSPDiagnostics — диагностика файла.
func (r *Registry) hLSPDiagnostics(_ context.Context, m map[string]any) (Result, error) {
	p, err := r.lspTarget(m)
	if err != nil {
		return Result{}, err
	}
	h := r.env.LSP
	if h == nil || !h.Supported(p) {
		return Result{}, lspUnavailable()
	}
	out, err := h.Diagnostics(r.env.WorkDir, p)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Text:    out,
		Summary: fmt.Sprintf("lsp: %s", filepath.Base(p)),
	}, nil
}

// hLSPHover — сигнатура и документация символа.
func (r *Registry) hLSPHover(_ context.Context, m map[string]any) (Result, error) {
	p, err := r.lspTarget(m)
	if err != nil {
		return Result{}, err
	}
	h := r.env.LSP
	if h == nil || !h.Supported(p) {
		return Result{}, lspUnavailable()
	}
	out, err := h.Hover(r.env.WorkDir, p, ArgInt(m, "line", 0), ArgInt(m, "character", 0))
	if err != nil {
		return Result{}, err
	}
	return Result{
		Text:    out,
		Summary: fmt.Sprintf("hover: %s", filepath.Base(p)),
	}, nil
}

// hLSPDefinition — куда ведёт символ.
func (r *Registry) hLSPDefinition(_ context.Context, m map[string]any) (Result, error) {
	p, err := r.lspTarget(m)
	if err != nil {
		return Result{}, err
	}
	h := r.env.LSP
	if h == nil || !h.Supported(p) {
		return Result{}, lspUnavailable()
	}
	out, err := h.Definition(r.env.WorkDir, p, ArgInt(m, "line", 0), ArgInt(m, "character", 0))
	if err != nil {
		return Result{}, err
	}
	return Result{
		Text:    out,
		Summary: fmt.Sprintf("definition: %s", filepath.Base(p)),
	}, nil
}
