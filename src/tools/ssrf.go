package tools

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// ---------- SSRF-защита ----------
//
// Зачем. URL в web_fetch приходит от модели: она берёт ссылку из вывода
// поиска, из README или из файла в репозитории и уходит по ней. Если адрес
// резолвится во внутреннюю сеть, запрос долетает туда, и ответ (часто с
// внутренними данными) попадает в контекст модели. Хуже того, это обход
// песочницы по сети: то, что файловая песочница не пускает, запрошенное
// по HTTP — пускает.
//
// Что проверяем. Схему (только http/https), сам IP, если он записан
// буквально, и — обязательно — DNS-ответ: «localhost» и «127.0.0.1» —
// одно и то же, а обычное имя в корпоративной сети спокойно резолвится в
// 10.x.x.x. Проверяем КАЖДЫЙ адрес из ответа, а не только первый: так
// переключения между несколькими A/AAAA-записями не обходят фильтр.

// allowLocalNetEnv — отключение проверки «извините, внутренний адрес».
//
// Один раз, на конкретный запуск: у разработчика, который локально гоняет
// свой сервис на :8080, web_fetch должен работать. Именно поэтому это
// переменная окружения, а не флаг в конфиге: забыть её в настройках
// должно быть легко, а включить её на каждый запуск — легко намеренно.
const allowLocalNetEnv = "GCLI_ALLOW_LOCAL_NET"

// LocalNetAllowed — разрешены ли внутренние адреса (проверка выключена).
func LocalNetAllowed() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(allowLocalNetEnv)))
	return v != "" && v != "0" && v != "false" && v != "off" && v != "нет"
}

// isPrivateIP — адрес, недоступный из внешней сети.
func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// Приводим 4-байтовый вид к 16-байтному явно: у ::ffff:127.0.0.1
	// IsLoopback() зависит от версии Go и иногда проходит мимо.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	switch {
	case ip.IsLoopback(), ip.IsUnspecified(), ip.IsPrivate(),
		ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(), ip.IsInterfaceLocalMulticast():
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		if len(v4) != 4 {
			return true
		}
		switch {
		case v4[0] == 0, v4[0] >= 240: // «this network» и 240.0.0.0/4
			return true
		case v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127: // CGNAT 100.64/10
			return true
		}
		return false
	}
	// Teredo (2001:0000::/32) и 6to4 (2002::/16) умеют пробрасывать
	// IPv4-адрес внутри IPv6-пакета, поэтому их тоже режем.
	if len(ip) == net.IPv6len && ip[0] == 0x20 && (ip[1] == 0x01 || ip[1] == 0x02) && ip[2] == 0x00 {
		return true
	}
	return false
}

// checkSSRF — можно ли делать запрос на этот адрес.
//
// Ошибка внятная и объясняет причину: агенту полезнее знать, что адрес
// внутренний, чем получить таймаут соединения и гадать.
func checkSSRF(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("плохой URL: %s", raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	case "":
		return fmt.Errorf("в URL нет схемы: %s", raw)
	case "file", "data", "gopher", "dict", "ftp", "ldap", "tftp", "jar":
		// Локальные и экзотические схемы — не «сетевые запросы», а чтение
		// произвольных источников. Для них есть свои инструменты.
		return fmt.Errorf("схема %q недоступна: используй http или https", u.Scheme)
	default:
		return fmt.Errorf("схема %q не поддерживается (только http/https)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("в URL нет хоста: %s", raw)
	}
	if LocalNetAllowed() {
		return nil
	}
	// Буквальный IP — решение без DNS.
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		if isPrivateIP(ip) {
			return fmt.Errorf("внутренний адрес запрещён: %s (%s)", host, ip)
		}
		return nil
	}
	// Имена, которые всегда внутренние, — без резолва. Часть из них
	// (metadata.google.internal) не резолвится вовсе, и без этого списка
	// запрос просто ушёл бы в никуда или, что хуже, отрезолвился бы
	// в чужой сети с тем же именем.
	lh := strings.ToLower(strings.TrimSuffix(host, "."))
	if isInternalName(lh) {
		return fmt.Errorf("внутренний адрес запрещён: %s", host)
	}
	// Не разрезолвилось — запрос НЕ разрешаем. Раньше здесь был `return nil`,
	// и это была дыра: любой нерезолвящийся (или специально сломанный) хост
	// проходил проверку, а ответ приходил уже с того адреса, который выбрал
	// резолвер в момент соединения. Правило простое: не знаем адреса — не
	// ходим.
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("не удалось разрешить %s: не иду на неизвестный адрес", host)
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return fmt.Errorf("хост %s резолвится во внутренний адрес (%s) — запрещён; "+
				"разрешить локальные адреса: %s=1", host, ip, allowLocalNetEnv)
		}
	}
	return nil
}

// ---------- Закрепление адреса на соединении ----------

// Проверки выше недостаточно: между ней и реальным соединением DNS отвечает
// заново и вполне может отдать другой адрес (DNS rebinding). Поэтому адрес
// проверяется ещё раз в момент подключения — уже тот, с которым мы говорим.
// Это единственная проверка, которую нельзя обойти подменой ответа DNS.

// ssrfDialContext — dialer, который не подключается к внутренним адресам.
//
// К адресу прокси из переменных окружения не применяем проверку: соединение
// идёт с прокси, а не с целью, и корпоративный прокси на 10.x.x.x — это
// нормально. Цель проверяется до запроса (checkSSRF).
func ssrfDialContext(base *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	allowed := proxyHosts()
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		host = strings.TrimSuffix(strings.ToLower(host), ".")
		if allowed[host] || LocalNetAllowed() {
			return base.DialContext(ctx, network, addr)
		}
		if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
			if isPrivateIP(ip) {
				return nil, fmt.Errorf("внутренний адрес запрещён: %s", ip)
			}
			return base.DialContext(ctx, network, addr)
		}
		// Имя: разрешаем сами и берём первый адрес. Так адрес, который
		// проверяется, и адрес, в который идём, — это всегда одно и то же.
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("не удалось разрешить %s: %w", host, err)
		}
		if len(addrs) == 0 {
			return nil, fmt.Errorf("хост %s не вернул ни одного адреса", host)
		}
		for _, a := range addrs {
			if isPrivateIP(a.IP) {
				return nil, fmt.Errorf("внутренний адрес запрещён: %s резолвится в %s", host, a.IP)
			}
		}
		d := &net.Dialer{Timeout: base.Timeout, KeepAlive: base.KeepAlive}
		return d.DialContext(ctx, network, net.JoinHostPort(addrs[0].IP.String(), port))
	}
}

// proxyHosts — хосты прокси из HTTP_PROXY/HTTPS_PROXY/ALL_PROXY.
func proxyHosts() map[string]bool {
	out := map[string]bool{}
	for _, env := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY",
		"http_proxy", "https_proxy", "all_proxy"} {
		raw := strings.TrimSpace(os.Getenv(env))
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "://") {
			raw = "http://" + raw
		}
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		if h := strings.ToLower(u.Hostname()); h != "" {
			out[h] = true
		}
	}
	return out
}

// internalNames — имена хостов, которые всегда указывают внутрь.
var internalNames = []string{
	"localhost", "ip6-localhost", "ip6-loopback",
	"metadata", "metadata.google.internal", "metadata.goog",
	"instance-data", "instance-data.ec2.internal",
}

// isInternalName — хост из списка внутренних или его поддомен.
func isInternalName(host string) bool {
	for _, n := range internalNames {
		if host == n || strings.HasSuffix(host, "."+n) {
			return true
		}
	}
	return false
}
