package providers

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client — HTTP-клиент с общим таймаутом и ретраями.
type Client struct {
	HTTP *http.Client
	// Retryable — функция решает, стоит ли повторить запрос.
	Retries int
}

// NewClient — создать клиент.
func NewClient() *Client {
	return &Client{
		HTTP: &http.Client{
			Timeout: 0, // таймауты управляются через context
			Transport: &http.Transport{
				MaxIdleConns:        32,
				MaxIdleConnsPerHost: 8,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		Retries: 3,
	}
}

// retryErr — ошибка, при которой можно повторить запрос (до первого байта потока).
type retryErr struct{ err error }

func (e *retryErr) Error() string { return e.err.Error() }
func (e *retryErr) Unwrap() error { return e.err }

// IsRetryable — можно ли повторить.
func IsRetryable(err error) bool {
	var re *retryErr
	return asRetry(err, &re)
}

func asRetry(err error, target **retryErr) bool {
	if err == nil {
		return false
	}
	if re, ok := err.(*retryErr); ok {
		*target = re
		return true
	}
	return false
}

// streamFunc — обработчик одного SSE-события (payload без "data: ").
type streamFunc func(data string) error

// doSSE — выполнить запрос и разобрать поток Server-Sent Events.
//
// Специфика: OpenAI-совместимые endpoint-ы и Anthropic отдают "data: {json}\n\n",
// где у всех одна и та же структура. Обрабатываем общий случай + [DONE].
// Корректные коды ошибок HTTP превращаются в retryErr для кодов 429/5xx.
func (c *Client) doSSE(ctx context.Context, req *http.Request, onData streamFunc) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return &retryErr{err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			msg = "(пустой ответ)"
		}
		e := fmt.Errorf("HTTP %d: %s", resp.StatusCode, coreTruncate(oneLine(msg), 300))
		switch {
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			return fmt.Errorf("%w — ключ отклонён: тот ли это ключ для этого провайдера? нет ли кавычек/«Bearer»? диагностика: /doctor key", e)
		case resp.StatusCode == 402:
			return fmt.Errorf("%w — на балансе провайдера нет средств — пополни аккаунт (тест: /doctor key)", e)
		case resp.StatusCode == 404:
			return fmt.Errorf("%w — неверный base URL или имя модели — проверь: /provider list · /models", e)
		case resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500:
			return &retryErr{e}
		default:
			return e
		}
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	gotAny := false
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		gotAny = true
		if err := onData(data); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if err := sc.Err(); err != nil {
		if !gotAny {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return &retryErr{fmt.Errorf("поток оборвался до первого события: %v", err)}
		}
		return fmt.Errorf("ошибка чтения потока: %w", err)
	}
	if !gotAny {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return &retryErr{fmt.Errorf("пустой поток от API (проверь базовый URL и модель)")}
	}
	return nil
}

// coreTruncate — обрезка без разрыва UTF-8.
func coreTruncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 3 {
		return string(r[:n])
	}
	return string(r[:n-3]) + "..."
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")), " ")
}
