package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gcli/core"
)

// ThinkState — нормализованный режим размышлений.
func ThinkState(cfg core.Config) string {
	switch cfg.Think {
	case "on", "off":
		return cfg.Think
	}
	return "auto"
}

// Stream — единая точка стриминга: выбирает протокол провайдера.
func Stream(ctx context.Context, c *Client, p *Provider, creq core.ChatRequest, think string, out chan<- core.Delta) error {
	if p == nil {
		return fmt.Errorf("провайдер не задан")
	}
	if creq.Model == "" {
		return fmt.Errorf("не выбрана модель — задай: /model <имя> или пройди /setup")
	}
	if !p.HasKey() {
		return fmt.Errorf("нет API-ключа для %s. Настрой: /setup (мастер) или /provider key %s <ключ>; либо env: %s",
			p.Label, p.ID, strings.Join(p.KeyEnvs, ", "))
	}
	if p.BaseURL == "" {
		return fmt.Errorf("у провайдера %s не задан base URL — /provider add %s <url>", p.ID, p.ID)
	}
	if p.Kind == ProtoAnthropic {
		return StreamAnthropic(ctx, c, p, creq, think, out)
	}
	return StreamOpenAI(ctx, c, p, creq, think, out)
}

// FetchModels — список моделей с endpoint-а: GET {base}/models.
func FetchModels(ctx context.Context, c *Client, p *Provider) ([]string, error) {
	if p == nil {
		return nil, fmt.Errorf("провайдер не задан")
	}
	if p.BaseURL == "" {
		return nil, fmt.Errorf("у провайдера %s не задан base URL", p.ID)
	}
	if c == nil || c.HTTP == nil {
		return nil, fmt.Errorf("нет HTTP-клиента для запроса к провайдеру")
	}
	base := p.BaseURL
	if p.Kind == ProtoAnthropic {
		base = anthropicBase(base)
	} else {
		base = strings.TrimRight(base, "/")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	// Список моделей идёт с перебором ключей: мёртвый первый ключ не
	// должен ронять /models.
	_, body, err := c.fetchWithKeys(ctx, p, func(key string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
		if err != nil {
			return nil, err
		}
		p.applyKey(req, key)
		for k, v := range p.ExtraHeaders {
			req.Header.Set(k, v)
		}
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	var ids []string
	var obj struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &obj); err == nil && len(obj.Data) > 0 {
		for _, m := range obj.Data {
			if m.ID != "" {
				ids = append(ids, m.ID)
			}
		}
	} else {
		var arr []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &arr); err == nil {
			for _, m := range arr {
				if m.ID != "" {
					ids = append(ids, m.ID)
				}
			}
		}
	}
	if len(ids) == 0 {
		// Некоторые сервисы отдают {"models":[{"name":...}]} — пробуем.
		var alt struct {
			Models []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &alt); err == nil {
			for _, m := range alt.Models {
				id := m.ID
				if id == "" {
					id = m.Name
				}
				if id != "" {
					ids = append(ids, id)
				}
			}
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("endpoint не вернул моделей (формат не распознан)")
	}
	sort.Strings(ids)
	return ids, nil
}

// reHTTPStatus — код HTTP из текста ошибки.
var reHTTPStatus = regexp.MustCompile(`HTTP (\d{3})`)

// TestKey — реальная проверка ключей запросом GET /models.
// Возвращает (ok, человекочитаемое объяснение).
//
// При пуле ключей проверяются все: человек должен видеть, какой ключ
// годен, а какой уже отвергнут провайдером. Иначе пришлось бы гадать,
// почему после смены ключа провайдер всё ещё не пускает.
func TestKey(c *Client, p *Provider) (bool, string) {
	if p == nil {
		return false, "провайдер не задан"
	}
	if p.NoKey {
		return true, "локальный провайдер — ключ не требуется"
	}
	if p.KeyCount() == 0 {
		return false, "ключ не задан — задай: /provider key " + p.ID + " <ключ> или /setup"
	}
	keys := p.Pool.Keys()
	if len(keys) == 0 {
		keys = []string{p.Key}
	}
	var (
		good    []string
		problem []string
		lastErr error
	)
	for _, k := range keys {
		err := testOneKey(c, p, k)
		if err == nil {
			good = append(good, k)
			continue
		}
		lastErr = err
		problem = append(problem, k)
	}
	switch {
	case len(good) > 0:
		msg := "рабочих ключей: " + strconv.Itoa(len(good)) + " из " + strconv.Itoa(len(keys))
		if len(good) == 1 {
			msg = "ключ " + MaskKey(good[0]) + " рабочий"
		}
		if len(problem) > 0 {
			msg += "; не прошли: " + strconv.Itoa(len(problem))
		}
		return true, msg
	case len(problem) > 0:
		return false, keyProblemText(p, lastErr, len(problem))
	}
	return false, "проверка не удалась"
}

// testOneKey — проверка одного ключа.
func testOneKey(c *Client, p *Provider, key string) error {
	probe := *p
	probe.Pool = NewKeyPool([]string{key})
	_, err := FetchModels(context.Background(), c, &probe)
	return err
}

// keyProblemText — объяснение по последней ошибке проверки ключа.
func keyProblemText(p *Provider, err error, count int) string {
	if err == nil {
		return "ключ не прошёл проверку"
	}
	msg := oneLine(err.Error())
	m := reHTTPStatus.FindStringSubmatch(msg)
	status := ""
	if m != nil {
		status = m[1]
	}
	switch status {
	case "401":
		return "401 — сервер отклонил ключ: чужой для этого провайдера или с опечаткой. " +
			"Вставь заново: /provider key " + p.ID + " <ключ>"
	case "403":
		return "403 — доступ запрещён: у ключа нет прав на этот endpoint либо регион заблокирован"
	case "402":
		return "402 — на балансе провайдера нет средств — пополни аккаунт (ключ при этом правильный)"
	case "404":
		base := strings.TrimRight(p.BaseURL, "/")
		if p.Kind == ProtoAnthropic {
			base = anthropicBase(p.BaseURL)
		}
		return "404 — base URL неверный: " + base + "/models не найден — исправь: /provider add " + p.ID + " <url>"
	case "429":
		return "429 — лимит запросов: ключ живой, но квота исчерпана — подожди и повтори"
	}
	low := strings.ToLower(msg)
	for _, s := range []string{"deadline", "timeout", "connection refused", "no such host", "dial tcp", "network is unreachable"} {
		if strings.Contains(low, s) {
			return "сеть недоступна или DNS не резолвит " + p.BaseURL + " — " + coreTruncate(msg, 80)
		}
	}
	if count > 1 {
		return "ни один ключ из " + strconv.Itoa(count) + " не прошёл: " + coreTruncate(msg, 100)
	}
	return coreTruncate(msg, 120)
}

// Ping — быстрая проверка доступности endpoint-а.
func Ping(c *Client, p *Provider) (time.Duration, int, error) {
	if p == nil {
		return 0, 0, fmt.Errorf("провайдер не задан")
	}
	if p.BaseURL == "" {
		return 0, 0, fmt.Errorf("у провайдера %s не задан base URL", p.ID)
	}
	if c == nil || c.HTTP == nil {
		return 0, 0, fmt.Errorf("нет HTTP-клиента для запроса к провайдеру")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	t0 := time.Now()
	// Пинг тоже идёт с перебором ключей: недоступный ключ не должен
	// выглядеть как недоступный сервер.
	status, _, err := c.fetchWithKeys(ctx, p, func(key string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.BaseURL, "/"), nil)
		if err != nil {
			return nil, err
		}
		p.applyKey(req, key)
		for k, v := range p.ExtraHeaders {
			req.Header.Set(k, v)
		}
		return req, nil
	})
	el := time.Since(t0)
	if err != nil {
		return el, status, err
	}
	return el, status, nil
}
