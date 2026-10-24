package providers

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client — HTTP-клиент для запросов к провайдерам (общий для всех горутин).
type Client struct {
	HTTP *http.Client
}

// NewClient — создать клиент.
//
// Общий Timeout не задаётся: поток живёт минутами, и его срок управляется
// контекстом вызывающего. Но зависание на этапе соединения/заголовков
// ограничено отдельными таймаутами транспорта, иначе недоступный сервер
// держал бы горутину до отмены контекста.
func NewClient() *Client {
	return &Client{
		HTTP: &http.Client{
			Timeout: 0, // таймауты управляются через context
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConns:          32,
				MaxIdleConnsPerHost:   8,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 60 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
				ForceAttemptHTTP2:     true,
			},
		},
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
	return errors.As(err, target)
}

// keyError — запрос не прошёл из-за ключа: лимит, баланс или отказ.
//
// Отдельный тип нужен, чтобы отличить «ключ исчерпан» от «сервер упал»:
// в первом случае есть смысл переключиться на другой ключ, во втором —
// нет.
type keyError struct {
	err   error
	pause time.Duration
	bad   bool
	// reason — человекочитаемая причина (показывается в интерфейсе).
	reason string
}

func (e *keyError) Error() string { return e.err.Error() }
func (e *keyError) Unwrap() error { return e.err }

// keyErrorOf — достать ошибку ключа из цепочки.
func keyErrorOf(err error) (*keyError, bool) {
	var ke *keyError
	if errors.As(err, &ke) {
		return ke, true
	}
	return nil, false
}

// maxKeyAttempts — сколько ключей пробуем за один запрос.
//
// Пул бывает и на двадцать ключей, но запрос, который не проходит по
// пятнадцати ключам подряд, — это не лимит, а сломанный провайдер: дальше
// идти бессмысленно, и человек должен получить ошибку.
const maxKeyAttempts = 8

// streamWithKeys — стриминг с перебором ключей.
//
// Ключ может кончиться посреди работы: запрос не проходит, и без
// следующего ключа сессия встала бы. Здесь ключи перебираются по кругу,
// пока не кончатся или не упрётся в лимит попыток.
//
// Поворот возможен только ДО первого доставленного события. Если часть
// потока уже ушла получателю (текст, reasoning, вызовы инструментов), то
// перезапуск запроса другим ключом продублировал бы всё доставленное в
// контексте: модель увидела бы половину ответа дважды и продолжила бы её
// как новую. Поэтому ошибка после первого события — финальная, даже если
// по смыслу это «ключ исчерпан».
func (c *Client) streamWithKeys(ctx context.Context, p *Provider, req *http.Request, onData streamFunc) error {
	delivered := false
	guarded := func(data string) error {
		err := onData(data)
		if err != nil {
			if delivered {
				// Срываем метки retryErr/keyError: их наличие — сигнал
				// к повтору, а повтор здесь запрещён.
				return errors.New(err.Error())
			}
			return err
		}
		delivered = true
		return nil
	}
	for attempt := 0; ; attempt++ {
		err := c.doSSE(ctx, req, guarded)
		if err == nil {
			return nil
		}
		ke, ok := keyErrorOf(err)
		if !ok {
			return err
		}
		if p == nil || p.Pool == nil {
			return err
		}
		from, to, rotated := p.Pool.Rotate(ke.reason, ke.pause, ke.bad)
		if !rotated {
			// Пригодных ключей не осталось. Если все на паузе — говорим,
			// когда ждать: молчаливая ошибка «лимит запросов» оставляет
			// человека в догадках.
			if until, paused := p.Pool.PausedUntil(); paused {
				return fmt.Errorf("%w — все ключи на паузе до %s", err, until.Format("15:04:05"))
			}
			return err
		}
		if attempt+1 >= maxKeyAttempts {
			return fmt.Errorf("%w — ключей перебрано: %d", err, attempt+1)
		}
		req = withKey(req, p, p.CurrentKey())
		if p.OnRotate != nil {
			p.OnRotate(from, to, ke.reason)
		}
	}
}

// fetchWithKeys — короткий запрос с перебором ключей.
//
// Список моделей, проверка ключа и пинг идут не потоком, а обычным
// запросом: перебор ключей для них устроен так же, как в потоке, иначе
// мёртвый первый ключ ронял бы и /models, и /doctor key.
func (c *Client) fetchWithKeys(ctx context.Context, p *Provider, build func(key string) (*http.Request, error)) (int, []byte, error) {
	for attempt := 0; ; attempt++ {
		key := ""
		if p != nil {
			key = p.CurrentKey()
		}
		req, err := build(key)
		if err != nil {
			return 0, nil, err
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return 0, nil, ctxErr
			}
			return 0, nil, err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return resp.StatusCode, body, nil
		}
		e := fmt.Errorf("HTTP %d: %s", resp.StatusCode, coreTruncate(oneLine(string(body)), 300))
		ke, ok := keyErrorForStatus(resp.StatusCode, e)
		if !ok || p == nil || p.Pool == nil {
			return resp.StatusCode, body, e
		}
		from, to, rotated := p.Pool.Rotate(ke.reason, ke.pause, ke.bad)
		if !rotated {
			return resp.StatusCode, body, e
		}
		if attempt+1 >= maxKeyAttempts {
			return resp.StatusCode, body, fmt.Errorf("%w — ключей перебрано: %d", e, attempt+1)
		}
		if p.OnRotate != nil {
			p.OnRotate(from, to, ke.reason)
		}
	}
}

// keyErrorForStatus — ошибка ключа по коду ответа.
func keyErrorForStatus(status int, e error) (*keyError, bool) {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &keyError{err: e, bad: true, reason: "ключ отклонён"}, true
	case http.StatusPaymentRequired:
		return &keyError{err: e, pause: KeyPauseNoMoney, reason: "нет средств на балансе"}, true
	case http.StatusTooManyRequests:
		return &keyError{err: e, pause: KeyPauseLimit, reason: "лимит запросов"}, true
	}
	return nil, false
}

// withKey — копия запроса с заголовком другого ключа.
//
// Тело запроса переиспользуется: GetBody есть у запросов, созданных из
// bytes.Reader, и без него повторный запрос ушёл бы с пустым телом.
func withKey(req *http.Request, p *Provider, key string) *http.Request {
	if req == nil {
		return nil
	}
	out := req.Clone(req.Context())
	if req.GetBody != nil {
		if body, err := req.GetBody(); err == nil {
			out.Body = body
		}
	}
	p.applyKey(out, key)
	return out
}

// applyKey — поставить заголовок авторизации по протоколу провайдера.
func (p *Provider) applyKey(req *http.Request, key string) {
	if req == nil {
		return
	}
	if p.Kind == ProtoAnthropic {
		if key != "" {
			req.Header.Set("x-api-key", key)
		}
		req.Header.Set("anthropic-version", "2023-06-01")
		return
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
}

// streamFunc — обработчик одного SSE-события (payload без "data: ").
type streamFunc func(data string) error

// doSSE — выполнить запрос и разобрать поток Server-Sent Events.
//
// Специфика: OpenAI-совместимые endpoint-ы и Anthropic отдают "data: {json}\n\n",
// где у всех одна и та же структура. Обрабатываем общий случай + [DONE].
// Событие может состоять из нескольких строк "data:" (спецификация SSE
// склеивает их через перевод строки) и может не иметь завершающей пустой
// строки в конце потока.
// Корректные коды ошибок HTTP превращаются в retryErr для кодов 429/5xx.
func (c *Client) doSSE(ctx context.Context, req *http.Request, onData streamFunc) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if c == nil || c.HTTP == nil {
		return fmt.Errorf("нет HTTP-клиента для запроса к провайдеру")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// Отмена человеком — не сетевой сбой: повторять такой запрос нельзя.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
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
			// Ключ отвергнут: с ним больше нельзя, следующий может быть
			// годен. Сессия продолжается, если ключ в пуле есть.
			return &keyError{
				err:    fmt.Errorf("%w — ключ отклонён: тот ли это ключ для этого провайдера? нет ли кавычек/«Bearer»? диагностика: /doctor key", e),
				bad:    true,
				reason: "ключ отклонён",
			}
		case resp.StatusCode == 402:
			// Денег нет: ключ бесполезен до пополнения. Пауза длинная.
			return &keyError{
				err:    fmt.Errorf("%w — на балансе провайдера нет средств — пополни аккаунт (тест: /doctor key)", e),
				pause:  KeyPauseNoMoney,
				reason: "нет средств на балансе",
			}
		case resp.StatusCode == 404:
			return fmt.Errorf("%w — неверный base URL или имя модели — проверь: /provider list · /models", e)
		case resp.StatusCode == 429:
			// Лимит запросов: ключ уходит на минуту, запрос продолжается
			// со следующего. Ошибка остаётся повторяемой: если других
			// ключей нет, повтор с тем же ключом ещё может пройти.
			return &keyError{
				err:    &retryErr{fmt.Errorf("%w — лимит запросов исчерпан (тест: /doctor key)", e)},
				pause:  KeyPauseLimit,
				reason: "лимит запросов",
			}
		case resp.StatusCode == 408 || resp.StatusCode >= 500:
			return &retryErr{e}
		default:
			return e
		}
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var dataLines []string
	gotAny := false
	done := false
	// dispatch отдаёт накопленное событие обработчику. Пустые события
	// (data: без содержимого) пропускаются: JSON из них всё равно не
	// собрать, а «поток без событий» должен остаться повторяемым.
	dispatch := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		if data == "" {
			return nil
		}
		if data == "[DONE]" {
			done = true
			return nil
		}
		gotAny = true
		if err := onData(data); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}

	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			if done {
				break
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // комментарий/keepalive
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			field, value = line, ""
		}
		if field != "data" {
			continue
		}
		// По спецификации после двоеточия допускается один пробел.
		value = strings.TrimPrefix(value, " ")
		dataLines = append(dataLines, strings.TrimSpace(value))
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
	// Поток мог закончиться без завершающей пустой строки — последнее
	// событие всё равно обязано дойти до обработчика.
	if err := dispatch(); err != nil {
		return err
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
