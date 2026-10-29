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
	// trigger — подстроки, без которых правило совпасть не может.
	//
	// Нужны быстрому выходу looksSecret: он решает, стоит ли вообще
	// гонять регулярки, и решение принимается по подстрокам, а не разбором
	// регулярного выражения. Список жил отдельно от таблицы правил, и
	// разъехался с ней: в правила добавили «passwd» и «private-key», а в
	// список — нет, и эти формы уходили в текст целиком, молча и без
	// единого признака.
	//
	// Теперь триггер лежит рядом с регуляркой, а выводится из таблицы, так
	// что забыть его нельзя: TestEveryRuleHasTrigger падает на правиле без
	// триггера, а TestLooksSecretCoversEveryRule — на форме, которую
	// правило маскирует, а быстрый выход не видит.
	//
	// Все триггеры в нижнем регистре: сравнение идёт по уже приведённой
	// строке. Регистр в самих регулярках часть правил требует заглавных
	// (AKIA, AIza, ghp_), часть — нет ((?i) у присваиваний и JSON).
	// Лишнее срабатывание быстрого выхода на «akia» в нижнем регистре стоит
	// одного прохода регулярки, а вот незамеченный «PASSWORD=…» стоит
	// утечки: цена ошибки несимметрична, и выбор сделан в её пользу.
	trigger []string
}

// reSecret — правила маскирования.
var reSecret = []secretRule{
	// AWS: AKIA…, ASIA…, AIDA… — маскируем целиком.
	{regexp.MustCompile(`\b(?:AKIA|ASIA|AIDA|AROA|AGPA|ANPA|ANVA|APKA)[A-Z0-9]{16}\b`), 0,
		[]string{"akia", "asia", "aida", "aroa", "agpa", "anpa", "anva", "apka"}},
	// Google API: AIza…
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`), 0, []string{"aiza"}},
	// OpenAI / Anthropic / прочие sk-…
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{16,}\b`), 0, []string{"sk-"}},
	// GitHub: ghp_, gho_, ghu_, ghs_, ghr_, github_pat_
	{regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}\b`), 0,
		[]string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_"}},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`), 0, []string{"github_pat_"}},
	// GitLab: glpat-…
	{regexp.MustCompile(`\bglpat-[A-Za-z0-9_\-]{16,}\b`), 0, []string{"glpat-"}},
	// Slack: xox[baprs]-…
	{regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9\-]{10,}\b`), 0, []string{"xox"}},
	// npm_…
	{regexp.MustCompile(`\bnpm_[A-Za-z0-9]{30,}\b`), 0, []string{"npm_"}},
	// Telegram: 123456789:AA…
	{regexp.MustCompile(`\b\d{8,12}:AA[A-Za-z0-9_\-]{30,}\b`), 0, []string{":aa"}},
	// Присваивания «KEY=value». Имя-метка (группа 1) и разделитель (2)
	// сохраняются — чтобы было видно, ЧТО замаскировано; маскируется
	// значение (группа 4). Требование длины значения (8+) отсекает
	// «token=ok».
	{regexp.MustCompile(`(?i)\b([A-Za-z0-9_.\-]*(?:token|secret|passwd|password|api[_-]?key|apikey|auth|credential|private[_-]?key)[A-Za-z0-9_.\-]*)(\s*[:=]\s*)("?)([^\s"'` + "`" + `,;)\]}]{8,})`), 4,
		[]string{"token", "secret", "passwd", "password", "apikey", "api_key", "api-key",
			"auth", "credential", "private_key", "private-key"}},
	// Bearer / Basic в заголовке: маскируется сам токен (группа 3).
	{regexp.MustCompile(`(?i)\b(authorization\s*[:=]\s*)(bearer|basic)\s+([A-Za-z0-9._\-+/=]{12,})`), 3,
		[]string{"bearer", "basic"}},
	// JSON-поля "api_key": "…" — маскируется значение (группа 2).
	{regexp.MustCompile(`(?i)("(?:api[_-]?key|apikey|access[_-]?token|refresh[_-]?token|password|secret|private[_-]?key)"\s*:\s*")([^"]{8,})`), 2,
		[]string{"apikey", "api_key", "api-key", "accesstoken", "access_token", "access-token",
			"refreshtoken", "refresh_token", "refresh-token", "password", "secret",
			"private_key", "private-key"}},
	// PEM-блок приватного ключа целиком: заголовок, тело, END. Частичная
	// маскировка base64-тела бессмысленна — прячем блок целиком.
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.{16,}?-----END [A-Z ]*PRIVATE KEY-----`), 0,
		[]string{"private key"}},
	// Пароль в URL: https://user:пароль@host. Схема и пользователь
	// остаются видны, маскируется пароль.
	{regexp.MustCompile(`\b[a-z][a-z0-9+.\-]*://[^/\s:@]*:([^@\s/]{8,})@`), 2,
		[]string{"://"}},
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
//
// Триггеры берутся из таблицы правил, а не из отдельного списка: правило и
// его признаки лежат рядом, и добавить одно, забыв другое, уже нельзя.
// Проверка регистронезависимая — см. комментарий к secretRule.trigger,
// где объяснено, почему лишнее срабатывание дешевле пропуска.
func looksSecret(s string) bool {
	low := strings.ToLower(s)
	for _, rule := range reSecret {
		for _, t := range rule.trigger {
			if strings.Contains(low, t) {
				return true
			}
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
