package tools

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gcli/core"
)

// HTTPClient — сетевой клиент инструментов.
type HTTPClient struct {
	Client  *http.Client
	Version string
}

// NewHTTPClient — создать клиент с таймаутом и User-Agent.
//
// Транспорт с проверкой адреса: dialer не подключается к внутренним адресам,
// поэтому DNS-rebinding (ответ «внешний» на проверку, «127.0.0.1» на
// соединение) не проходит. Проверять в checkSSRF мало: между проверкой и
// подключением DNS отвечает заново.
func NewHTTPClient() *HTTPClient {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           ssrfDialContext(dialer),
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	}
	return &HTTPClient{
		Client:  &http.Client{Timeout: 30 * time.Second, Transport: tr},
		Version: core.Version,
	}
}

func (c *HTTPClient) userAgent() string {
	return "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) gcli/" + c.Version
}

// Регулярки разбора HTML.
var (
	reScript    = regexp.MustCompile(`(?is)<script.*?</script>|<style.*?</style>|<noscript.*?</noscript>|<svg.*?</svg>|<head.*?</head>`)
	reBlockTag  = regexp.MustCompile(`(?i)<br\s*/?>|</(p|div|h[1-6]|li|tr|section|article|blockquote|pre|td|th)>`)
	reTag       = regexp.MustCompile(`<[^>]*>`)
	reNumEntity = regexp.MustCompile(`&#(\d+);`)
	reHexEntity = regexp.MustCompile(`&#x([0-9a-fA-F]+);`)
	reNamedEnt  = map[string]string{
		"&amp;": "&", "&lt;": "<", "&gt;": ">", "&quot;": "\"", "&#39;": "'",
		"&apos;": "'", "&nbsp;": " ", "&laquo;": "«", "&raquo;": "»",
		"&mdash;": "—", "&ndash;": "–", "&hellip;": "…", "&middot;": "·",
		"&times;": "×", "&rarr;": "→", "&larr;": "←", "&rsquo;": "’", "&lsquo;": "‘",
		"&ldquo;": "“", "&rdquo;": "”", "&deg;": "°", "&plusmn;": "±", "&copy;": "©",
	}

	// DuckDuckGo HTML.
	reDDGResult  = regexp.MustCompile(`(?s)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	reDDGSnippet = regexp.MustCompile(`(?s)<a[^>]*class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</a>`)
	reDDGAlt     = regexp.MustCompile(`(?s)<a[^>]*class="[^"]*result__url[^"]*"[^>]*>(.*?)</a>`)
)

// DecodeEntities — декодировать HTML-сущности.
func DecodeEntities(s string) string {
	for k, v := range reNamedEnt {
		s = strings.ReplaceAll(s, k, v)
	}
	s = reNumEntity.ReplaceAllStringFunc(s, func(mm string) string {
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(mm, "&#"), ";"))
		if err != nil || n <= 0 || n > 0x10FFFF {
			return mm
		}
		return string(rune(n))
	})
	s = reHexEntity.ReplaceAllStringFunc(s, func(mm string) string {
		n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(mm, "&#x"), ";"), 16, 32)
		if err != nil || n <= 0 || n > 0x10FFFF {
			return mm
		}
		return string(rune(n))
	})
	return s
}

// StripTags — убрать HTML-теги.
func StripTags(s string) string { return strings.TrimSpace(reTag.ReplaceAllString(s, "")) }

// HTMLToText — извлечь читаемый текст из HTML.
func HTMLToText(h string) string {
	h = reScript.ReplaceAllString(h, " ")
	h = reBlockTag.ReplaceAllString(h, "\n")
	h = reTag.ReplaceAllString(h, "")
	h = DecodeEntities(h)
	var out []string
	prevEmpty := true
	for _, line := range strings.Split(h, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if prevEmpty {
				continue
			}
			prevEmpty = true
		} else {
			prevEmpty = false
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// hWebSearch — поиск в интернете.
func (r *Registry) hWebSearch(ctx context.Context, m map[string]any) (Result, error) {
	q := ArgStr(m, "query")
	if strings.TrimSpace(q) == "" {
		return Result{}, fmt.Errorf("укажи query")
	}
	maxRes := core.Clamp(ArgInt(m, "max_results", 8), 1, 20)

	searchCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	body, err := r.searchDDG(searchCtx, q)
	if err != nil {
		// Запасной путь — duckduckgo lite.
		if body2, err2 := r.searchLite(searchCtx, q); err2 == nil {
			body = body2
		} else {
			return Result{}, fmt.Errorf("поиск не удался (нет сети?): %v", err)
		}
	}

	matches := reDDGResult.FindAllStringSubmatch(body, maxRes)
	if len(matches) == 0 {
		return Result{
			Text:    fmt.Sprintf("По запросу «%s» ничего не найдено (или поисковик изменил разметку). Попробуй переформулировать или web_fetch напрямую.", q),
			Summary: "ничего не найдено",
		}, nil
	}
	snips := reDDGSnippet.FindAllStringSubmatch(body, maxRes)

	var b strings.Builder
	n := 0
	for i, mm := range matches {
		href := mm[1]
		if u, err := url.Parse(href); err == nil {
			if uddg := u.Query().Get("uddg"); uddg != "" {
				href = uddg
			}
		}
		if strings.HasPrefix(href, "//") {
			href = "https:" + href
		}
		title := DecodeEntities(StripTags(mm[2]))
		snip := ""
		if i < len(snips) {
			snip = DecodeEntities(StripTags(snips[i][1]))
		}
		fmt.Fprintf(&b, "%d. %s\n   %s\n", n+1, title, href)
		if snip != "" {
			fmt.Fprintf(&b, "   %s\n", core.OneLine(core.Truncate(snip, 220)))
		}
		n++
	}
	return Result{
		Text:    fmt.Sprintf("Результаты поиска «%s»:\n\n%s", q, b.String()),
		Summary: fmt.Sprintf("%d результатов", n),
	}, nil
}

func (r *Registry) searchDDG(ctx context.Context, q string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://html.duckduckgo.com/html/", strings.NewReader(url.Values{"q": {q}}.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", r.env.HTTPClient.userAgent())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r.doHTML(req)
}

func (r *Registry) searchLite(ctx context.Context, q string) (string, error) {
	u := "https://lite.duckduckgo.com/lite/?q=" + url.QueryEscape(q)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", r.env.HTTPClient.userAgent())
	return r.doHTML(req)
}

func (r *Registry) doHTML(req *http.Request) (string, error) {
	resp, err := r.env.HTTPClient.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 3<<20))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// hWebFetch — скачать страницу.
func (r *Registry) hWebFetch(ctx context.Context, m map[string]any) (Result, error) {
	raw := ArgStr(m, "url")
	if raw == "" {
		return Result{}, fmt.Errorf("укажи url")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return Result{}, fmt.Errorf("плохой URL: %s", raw)
	}
	// SSRF: модель ходит по ссылкам из вывода поиска и из файлов репозитория,
	// а ответ со внутреннего адреса утекает в контекст. Проверяем ДО запроса.
	if err := checkSSRF(raw); err != nil {
		return Result{}, err
	}
	if r.env.Confirm != nil && !r.env.Confirm(ConfirmReq{Kind: ConfirmNet, Detail: raw, Reason: "загрузка страницы"}) {
		return Result{Text: "Загрузка отменена пользователем", Summary: "отменено"}, nil
	}

	maxChars := core.Clamp(ArgInt(m, "max_chars", 8000), 500, 60000)
	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, raw, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", r.env.HTTPClient.userAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,*/*")

	resp, err := r.env.HTTPClient.Client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("загрузка не удалась: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("HTTP %d для %s", resp.StatusCode, raw)
	}
	ct := resp.Header.Get("Content-Type")
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	s := string(body)
	if strings.Contains(ct, "html") || ct == "" {
		s = HTMLToText(s)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return Result{}, fmt.Errorf("не удалось извлечь текст (content-type: %s)", ct)
	}
	note := ""
	if len([]rune(s)) > maxChars {
		s = string([]rune(s)[:maxChars])
		note = "\n\n[текст обрезан до max_chars]"
	}
	return Result{
		Text:    fmt.Sprintf("URL: %s\n\n%s%s", raw, s, note),
		Summary: fmt.Sprintf("%s — %s", hostOf(u), core.HumanSize(len(s))),
	}, nil
}

func hostOf(u *url.URL) string {
	h := u.Host
	if i := strings.IndexByte(h, ':'); i > 0 {
		h = h[:i]
	}
	return h
}
