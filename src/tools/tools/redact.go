package tools

import (
	"regexp"
	"strings"
)

// Сокрытие секретов в тексте, который уходит в контекст модели и в UI.
//
// Зачем. Агент регулярно натыкается на ключи: `.env`, config.json,
// ~/.npmrc, вывод `env` в CI, заголовки из curl. Дальше эти строки уходят
// в запрос к модели, попадают в журнал сессии и в /export. Третий человек,
// получивший лог, получает ключ.
//
// Что делаем. Маскируем значения по форме, а не по имени файла: ключ может
// называться как угодно, а форма у него узнаваемая. Правила сознательно
// широкие по форме и узкие по длине префикса, чтобы не превращать обычный
// текст в «***».
//
// Чего не делаем. Не маскируем короткие строки и хеши: `deadbeef`, «токен»,
// «ключ» в предложении — это не секрет, и замаскированный текст в отчёте
// мешает модели работать.

// secretRule — правило маскирования.
type secretRule struct {
	re *regexp.Regexp
	// value — номер группы со значением, которое маскируется. 0 = всё совпадение.
	//
	// Номер задан явно, а не «последняя непустая группа»: у присваивания
	// «KEY="value"» последняя группа — закрывающая кавычка, и маскирование
	// «последней» замаскировало бы кавычку вместо значения.
	value int
}

// reSecret — правила маскирования.
var reSecret = []secretRule{
	// AWS: AKIA…, ASIA…, AIDA… — маскируем целиком.
	{regexp.MustCompile(`\b(?:AKIA|ASIA|AIDA|AROA|AGPA|ANPA|ANVA|APKA)[A-Z0-9]{16}\b`), 0},
	// Google API: AIza…
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`), 0},
	// OpenAI / Anthropic / прочие sk-…
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{16,}\b`), 0},
	// GitHub: ghp_, gho_, ghu_, ghs_, ghr_, github_pat_
	{regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}\b`), 0},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`), 0},
	// GitLab: glpat-…
	{regexp.MustCompile(`\bglpat-[A-Za-z0-9_\-]{16,}\b`), 0},
	// Slack: xox[baprs]-…
	{regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9\-]{10,}\b`), 0},
	// npm_…
	{regexp.MustCompile(`\bnpm_[A-Za-z0-9]{30,}\b`), 0},
	// Telegram: 123456789:AA…
	{regexp.MustCompile(`\b\d{8,12}:AA[A-Za-z0-9_\-]{30,}\b`), 0},
	// Присваивания «KEY=value». Имя-метка (группа 1) и разделитель (2)
	// сохраняются — чтобы было видно, ЧТО замаскировано; маскируется
	// значение (группа 4). Требование длины значения (8+) отсекает
	// «token=ok».
	{regexp.MustCompile(`(?i)\b([A-Za-z0-9_.\-]*(?:token|secret|passwd|password|api[_-]?key|apikey|auth|credential|private[_-]?key)[A-Za-z0-9_.\-]*)(\s*[:=]\s*)("?)([^\s"'` + "`" + `,;)\]}]{8,})`), 4},
	// Bearer / Basic в заголовке: маскируется сам токен (группа 3).
	{regexp.MustCompile(`(?i)\b(authorization\s*[:=]\s*)(bearer|basic)\s+([A-Za-z0-9._\-+/=]{12,})`), 3},
	// JSON-поля "api_key": "…" — маскируется значение (группа 2).
	{regexp.MustCompile(`(?i)("(?:api[_-]?key|apikey|access[_-]?token|refresh[_-]?token|password|secret|private[_-]?key)"\s*:\s*")([^"]{8,})`), 2},
}

// secretMask — чем заменяем значение.
const secretMask = "[скрыто: секрет]"

// RedactSecrets — замаскировать секреты в тексте.
//
// Вызывается на всём, что покидает инструмент: текст ответа, вывод команды,
// содержимое прочитанного файла. Иначе утечка происходит через самый
// безобидный на вид инструмент — grep по «api_key», который честно покажет
// строку с ключом.
func RedactSecrets(s string) string {
	if s == "" {
		return s
	}
	// Быстрый выход: если в тексте нет даже намёка на секрет, не гоняем
	// десяток регулярок по каждому ответу bash.
	if !looksSecret(s) {
		return s
	}
	for _, rule := range reSecret {
		s = rule.re.ReplaceAllStringFunc(s, func(m string) string {
			return maskMatch(rule, m)
		})
	}
	return s
}

// looksSecret — дешёвая проверка «стоит ли вообще проверять».
func looksSecret(s string) bool {
	low := strings.ToLower(s)
	if strings.Contains(low, "token") || strings.Contains(low, "secret") ||
		strings.Contains(low, "password") || strings.Contains(low, "api_key") ||
		strings.Contains(low, "apikey") || strings.Contains(low, "api-key") ||
		strings.Contains(low, "bearer ") || strings.Contains(low, "basic ") ||
		strings.Contains(low, "credential") || strings.Contains(low, "private_key") ||
		strings.Contains(low, "auth") {
		return true
	}
	// Формы, не привязанные к слову-маркеру. Список обязан покрывать
	// ВСЕ префиксы из reSecret: быстрый выход не маскирует ничего, а
	// незаметный пропуск правила тихо оставлял секрет в тексте.
	for _, p := range []string{
		"sk-", "AKIA", "ASIA", "AIDA", "AROA", "AGPA", "ANPA", "ANVA", "APKA",
		"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-",
		"xox", "npm_", "AIza", "authorization", ":AA",
	} {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// maskMatch — замаскировать значение в одном совпадении, сохранив структуру.
//
// Имя правила и разделитель остаются видны («api_key: [скрыто: секрет]»),
// чтобы модель знала, ЧТО нашла, но не знала значения. Для правил без групп
// (AWS-ключи, sk-…) маскируется всё совпадение.
func maskMatch(rule secretRule, m string) string {
	if rule.value == 0 {
		return secretMask
	}
	idx := rule.re.FindStringSubmatchIndex(m)
	if idx == nil || 2*rule.value+1 >= len(idx) {
		return secretMask
	}
	s, e := idx[2*rule.value], idx[2*rule.value+1]
	if s < 0 || e <= s {
		return secretMask
	}
	return m[:s] + secretMask + m[e:]
}
