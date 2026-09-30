package tools

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// ---------- SSRF ----------

func TestSSRFBlocksInternal(t *testing.T) {
	blocked := []string{
		"http://127.0.0.1/",
		"http://127.0.0.1:8080/admin",
		"https://localhost/",
		"http://localhost:3000/metrics",
		"http://[::1]:8080/",
		"http://10.0.0.5/",
		"http://192.168.1.1/router",
		"http://172.16.0.1/",
		"http://169.254.169.254/latest/meta-data/", // метаданные облака
		"http://0.0.0.0/",
		"http://[fd00::1]/",
		"http://[fe80::1]/",
		"http://100.64.0.1/",
		"http://metadata.google.internal/computeMetadata/v1/",
	}
	for _, u := range blocked {
		if err := checkSSRF(u); err == nil {
			t.Errorf("внутренний адрес %s должен быть запрещён", u)
		}
	}
}

func TestSSRFAllowsPublic(t *testing.T) {
	// Публичные адреса проверяются без сети: литеральный IP резолвить не надо.
	ok := []string{
		"https://example.com/",
		"http://8.8.8.8/",
		"https://93.184.216.34/path?q=1",
		"https://[2606:2800:220:1:248:1893:25c8:1946]/",
	}
	for _, u := range ok {
		if err := checkSSRF(u); err != nil {
			t.Errorf("публичный адрес %s не должен запрещаться: %v", u, err)
		}
	}
}

func TestSSRFBlocksNonHTTPSchemes(t *testing.T) {
	// Схемы, которые читают произвольные источники, — не «сетевой запрос».
	for _, u := range []string{"file:///C:/Windows/win.ini", "data:text/html,<b>x</b>"} {
		if err := checkSSRF(u); err == nil {
			t.Errorf("схема в %s должна быть запрещена", u)
		}
	}
	if err := checkSSRF("ftp://example.com/x"); err == nil {
		t.Error("ftp должен быть запрещён")
	}
	if err := checkSSRF("https://"); err == nil {
		t.Error("URL без хоста должен быть отклонён")
	}
}

// TestSSRFRespectsOptOut — разработчик с локальным сервисом должен уметь
// отключить проверку, иначе инструмент бесполезен для его работы.
func TestSSRFRespectsOptOut(t *testing.T) {
	t.Setenv(allowLocalNetEnv, "1")
	if err := checkSSRF("http://127.0.0.1:8080/"); err != nil {
		t.Fatalf("с %s=1 внутренний адрес должен быть разрешён: %v", allowLocalNetEnv, err)
	}
	t.Setenv(allowLocalNetEnv, "0")
	if err := checkSSRF("http://127.0.0.1:8080/"); err == nil {
		t.Fatalf("с %s=0 внутренний адрес должен снова запрещаться", allowLocalNetEnv)
	}
}

// TestSSRFDialBlocksInternalIP — адрес проверяется в момент подключения, а не
// только до запроса: иначе ответ DNS, подменивший адрес между проверкой и
// соединением (rebinding), проходил бы фильтр.
func TestSSRFDialBlocksInternalIP(t *testing.T) {
	t.Setenv(allowLocalNetEnv, "")
	for _, env := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY",
		"http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(env, "")
	}
	dial := ssrfDialContext(&net.Dialer{Timeout: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, addr := range []string{"127.0.0.1:80", "10.0.0.1:443", "169.254.169.254:80", "[::1]:80"} {
		if conn, err := dial(ctx, "tcp", addr); err == nil {
			conn.Close()
			t.Errorf("соединение с %s должно запрещаться", addr)
		}
	}
}

// TestSSRFFailsClosedOnUnknownHost — неразрешившееся имя не должно молча
// проходить: раньше ошибка DNS возвращала «разрешено», и запрос уходил на
// адрес, о котором мы ничего не знаем.
func TestSSRFFailsClosedOnUnknownHost(t *testing.T) {
	t.Setenv(allowLocalNetEnv, "")
	err := checkSSRF("http://нет-такого-хоста-1234567.invalid/")
	if err == nil {
		t.Fatal("нерезолвившееся имя должно отклоняться, а не проходить")
	}
}

func TestSSRFWebFetchBlocksInternal(t *testing.T) {
	t.Setenv(allowLocalNetEnv, "")
	work := t.TempDir()
	r := New(Env{WorkDir: work})
	// Никакой сети тут быть не должно: проверка обязана сработать ДО запроса.
	if _, err := r.hWebFetch(nil, map[string]any{"url": "http://169.254.169.254/latest/meta-data/"}); err == nil {
		t.Fatal("web_fetch обязан отклонить метаданные облака")
	}
	if _, err := r.hWebFetch(nil, map[string]any{"url": "http://localhost:1/"}); err == nil {
		t.Fatal("web_fetch обязан отклонить localhost")
	}
}

func TestSSRFExtHTTPToolBlocksInternal(t *testing.T) {
	t.Setenv(allowLocalNetEnv, "")
	work := t.TempDir()
	r := New(Env{WorkDir: work})
	ext := Extension{Name: "внутренний"}
	tool := ExtTool{Name: "local", URL: "http://127.0.0.1:9000/{input}", Method: "GET"}
	h := r.extHTTPHandler(ext, tool)
	if _, err := h(nil, map[string]any{"input": "admin"}); err == nil {
		t.Fatal("HTTP-расширение не должно обходить SSRF-фильтр")
	}
}

// ---------- redaction ----------

func TestRedactKnownKeyForms(t *testing.T) {
	cases := []struct {
		name string
		in   string
		// что обязано исчезнуть
		secret string
		// что обязано остаться (чтобы было видно, ЧТО замаскировано)
		keep string
	}{
		{"aws", "key = AKIAIOSFODNN7EXAMPLE here", "AKIAIOSFODNN7EXAMPLE", "key ="},
		{"github", "ghp_abcdefghijklmnopqrstuvwxyz0123456789", "ghp_abcdefghijklmnopqrstuvwxyz0123456789", ""},
		{"openai", "OPENAI_API_KEY=sk-abcdefghijklmnopqrstuvwx", "sk-abcdefghijklmnopqrstuvwx", "OPENAI_API_KEY="},
		{"gitlab", "glpat-ABCDEFGHIJKLMNOPQRST", "glpat-ABCDEFGHIJKLMNOPQRST", ""},
		{"slack", "xoxb-1234567890-abcdefghijkl", "xoxb-1234567890-abcdefghijkl", ""},
		{"bearer", "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload", "eyJhbGciOiJIUzI1NiJ9.payload", "Authorization:"},
		{"json", `{"api_key": "abcdefghij12345678"}`, "abcdefghij12345678", "api_key"},
		{"env", "DB_PASSWORD=sup3rs3cret!", "sup3rs3cret!", "DB_PASSWORD="},
		{"google", "AIzaSyA1234567890abcdefghijklmnopqrstuv", "AIzaSyA1234567890abcdefghijklmnopqrstuv", ""},
		// Формы без слова-маркера: их ловит быстрый путь looksSecret, и если
		// он о префикс забыл, секрет уходил в текст целиком.
		{"telegram", "123456789:AAHqwertyuiopasdfghjklzxcvbnm1234", "AAHqwertyuiopasdfghjklzxcvbnm1234", ""},
		{"aws-role", "ARN AROAEXAMPLE123456789 ok", "AROAEXAMPLE123456789", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedactSecrets(c.in)
			if strings.Contains(got, c.secret) {
				t.Fatalf("секрет утек: %q", got)
			}
			if !strings.Contains(got, secretMask) {
				t.Fatalf("нет пометки о маскировании: %q", got)
			}
			if c.keep != "" && !strings.Contains(got, c.keep) {
				t.Fatalf("потерян контекст «%s»: %q", c.keep, got)
			}
		})
	}
}

func TestRedactKeepsOrdinaryText(t *testing.T) {
	// Ложные срабатывания — тоже поломка: замаскированный обычный текст
	// мешает модели работать, и агент начинает «чинить» несуществующую проблему.
	ordinary := []string{
		"package main\nfunc main() { println(\"привет\") }",
		"ключ = 42",
		"token=ok",
		"переменная tokenize и passwordless аутентификация",
		"func (c *Config) GetToken(ctx context.Context) string { return c.token }",
		"// авторизация пользователя описана в README",
	}
	for _, s := range ordinary {
		if got := RedactSecrets(s); got != s {
			t.Errorf("обычный текст изменён:\n было: %q\n стало: %q", s, got)
		}
	}
}

func TestRedactEmpty(t *testing.T) {
	if got := RedactSecrets(""); got != "" {
		t.Fatalf("пустая строка должна остаться пустой, стало %q", got)
	}
}
