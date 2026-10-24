package tools

import (
	"net/http"
	"net/url"
	"testing"
)

// TestHostOfIPv6 — ручное отрезание порта по первому «:» ломало IPv6:
// из «[::1]:8080» получался «[». Hostname разбирает адрес правильно.
func TestHostOfIPv6(t *testing.T) {
	u, err := url.Parse("http://[::1]:8080/admin")
	if err != nil {
		t.Fatal(err)
	}
	if got := hostOf(u); got != "::1" {
		t.Errorf("hostOf = %q, ожидалось ::1", got)
	}
}

// TestHTTPClientRevalidatesRedirects — внешний сайт может перенаправить
// запрос во внутреннюю сеть. Редирект обязан проходить ту же SSRF-проверку,
// что и исходный URL.
func TestHTTPClientRevalidatesRedirects(t *testing.T) {
	t.Setenv(allowLocalNetEnv, "")
	c := NewHTTPClient()
	u, err := url.Parse("http://127.0.0.1/admin")
	if err != nil {
		t.Fatal(err)
	}
	req := &http.Request{URL: u}
	if err := c.Client.CheckRedirect(req, nil); err == nil {
		t.Fatal("редирект на внутренний адрес не был отклонён")
	}
}
