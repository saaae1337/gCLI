package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, err
	}
	if p.Kind == ProtoAnthropic {
		req.Header.Set("x-api-key", p.Key)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else if p.Key != "" {
		req.Header.Set("Authorization", "Bearer "+p.Key)
	}
	for k, v := range p.ExtraHeaders {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, coreTruncate(oneLine(string(body)), 160))
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

// TestKey — реальная проверка ключа запросом GET /models.
// Возвращает (ok, человекочитаемое объяснение).
func TestKey(c *Client, p *Provider) (bool, string) {
	if p.NoKey {
		return true, "локальный провайдер — ключ не требуется"
	}
	if p.Key == "" {
		return false, "ключ не задан — задай: /provider key " + p.ID + " <ключ> или /setup"
	}
	_, err := FetchModels(context.Background(), c, p)
	if err == nil {
		return true, "сервер принял ключ " + MaskKey(p.Key) + " — он рабочий"
	}
	msg := oneLine(err.Error())
	m := reHTTPStatus.FindStringSubmatch(msg)
	status := ""
	if m != nil {
		status = m[1]
	}
	switch status {
	case "401":
		return false, "401 — сервер отклонил ключ: чужой для этого провайдера или с опечаткой. " +
			"Вставь заново: /provider key " + p.ID + " <ключ>"
	case "403":
		return false, "403 — доступ запрещён: у ключа нет прав на этот endpoint либо регион заблокирован"
	case "402":
		return false, "402 — на балансе провайдера нет средств — пополни аккаунт (ключ при этом правильный)"
	case "404":
		return false, "404 — base URL неверный: " + p.BaseURL + "/models не найден — исправь: /provider add " + p.ID + " <url>"
	case "429":
		return false, "429 — лимит запросов: ключ живой, но квота исчерпана — подожди и повтори"
	}
	low := strings.ToLower(msg)
	for _, s := range []string{"deadline", "timeout", "connection refused", "no such host", "dial tcp", "network is unreachable"} {
		if strings.Contains(low, s) {
			return false, "сеть недоступна или DNS не резолвит " + p.BaseURL + " — " + coreTruncate(msg, 80)
		}
	}
	return false, coreTruncate(msg, 120)
}

// Ping — быстрая проверка доступности endpoint-а.
func Ping(c *Client, p *Provider) (time.Duration, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.BaseURL, "/"), nil)
	if err != nil {
		return 0, 0, err
	}
	if p.Key != "" {
		if p.Kind == ProtoAnthropic {
			req.Header.Set("x-api-key", p.Key)
		} else {
			req.Header.Set("Authorization", "Bearer "+p.Key)
		}
	}
	for k, v := range p.ExtraHeaders {
		req.Header.Set(k, v)
	}
	t0 := time.Now()
	resp, err := c.HTTP.Do(req)
	el := time.Since(t0)
	if err != nil {
		return el, 0, err
	}
	defer resp.Body.Close()
	return el, resp.StatusCode, nil
}
