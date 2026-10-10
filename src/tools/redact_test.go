package tools

import (
	"strings"
	"testing"
)

// ---------- быстрый выход ----------

// TestLooksSecretCoversEveryRule — инвариант, который держал правила и
// быстрый выход вместе.
//
// looksSecret решает, стоит ли вообще гонять регулярки. Если он о правиле
// «не знает», выход срабатывает и секрет уходит в текст целиком — молча,
// без единого признака. Список слов для быстрого выхода жил отдельно от
// таблицы правил, и разъехался с ней: в правилах появились «passwd» и
// «private-key», а в список — нет.
//
// Здесь для каждого правила взят минимальный текст, который правило
// точно замаскирует, и проверяется, что быстрый выход на нём срабатывает.
// Правило без такого образца в таблице не может быть забыто молча: тест
// упадёт на нём сам.
func TestLooksSecretCoversEveryRule(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"aws", "AKIAIOSFODNN7EXAMPLE"},
		{"aws-role", "AROAEXAMPLE123456789"},
		{"google", "AIzaSyA1234567890abcdefghijklmnopqrstuv"},
		{"openai", "sk-abcdefghijklmnopqrstuvwx"},
		{"github", "ghp_abcdefghijklmnopqrstuvwxyz0123456789"},
		{"github-fine-grained", "github_pat_abcdefghijklmnopqrstuvwxyz"},
		{"gitlab", "glpat-ABCDEFGHIJKLMNOPQRST"},
		{"slack", "xoxb-1234567890-abcdefghijkl"},
		{"npm", "npm_abcdefghijklmnopqrstuvwxyz0123456789"},
		{"telegram", "123456789:AAHqwertyuiopasdfghjklzxcvbnm1234"},
		{"присваивание-токен", "token=abcdefghij"},
		{"присваивание-passwd", "passwd=abcdefghij"},
		{"присваивание-password", "password=abcdefghij"},
		{"присваивание-private-key", "private_key=abcdefghij"},
		{"присваивание-private-key-через-дефис", "private-key=abcdefghij"},
		{"присваивание-апи-ключ", "api_key=abcdefghij"},
		{"присваивание-credential", "credential=abcdefghij"},
		{"bearer", "Authorization: Bearer abcdefghijkl"},
		{"basic", "Authorization: Basic YWxhZGRpbjpvcGVuc2VzYW1l"},
		{"json-токен", `{"access_token": "abcdefghij"}`},
		{"json-пароль", `{"password": "abcdefghij"}`},
		{"json-private-key", `{"private_key": "abcdefghij"}`},
		{"json-private-key-через-дефис", `{"private-key": "abcdefghij"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !looksSecret(c.in) {
				t.Fatalf("быстрый выход не заметил %q: правило замаскирует это, "+
					"но RedactSecrets вернёт текст как есть и секрет утечёт", c.in)
			}
		})
	}
}

// TestEveryRuleHasTrigger — правило без триггера не может быть замечено
// быстрым выходом, то есть выключено целиком. Забытый триггер — это
// молча отключённая защита, и ловить его надо структурно, а не просмотром
// глазами: правила добавляются регулярно.
func TestEveryRuleHasTrigger(t *testing.T) {
	for i, r := range reSecret {
		if len(r.trigger) == 0 {
			t.Errorf("правило %d (%s) без триггера: быстрый выход его не увидит",
				i, r.re.String())
		}
		for _, tr := range r.trigger {
			if tr == "" {
				t.Errorf("правило %d (%s): пустой триггер", i, r.re.String())
			}
			if tr != strings.ToLower(tr) {
				t.Errorf("правило %d (%s): триггер %q не в нижнем регистре — "+
					"сравнение идёт по уже приведённой строке и не сработает",
					i, r.re.String(), tr)
			}
		}
	}
}

// TestRedactPasswdAndHyphenatedPrivateKey — конкретные формы, которые
// утекали в открытом виде: «passwd» есть в правиле присваивания, но не
// было в списке быстрого выхода, а «private-key» не совпадало с проверкой
// «private_key».
func TestRedactPasswdAndHyphenatedPrivateKey(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		secret string
	}{
		{"passwd", "passwd=hunter2000", "hunter2000"},
		{"passwd-верхний-регистр", "DB_PASSWD=hunter2000", "hunter2000"},
		{"passwd-в-кавычках", `passwd = "hunter2000"`, "hunter2000"},
		{"private-key-через-дефис", "private-key=BEGINKEY123", "BEGINKEY123"},
		{"json-private-key-через-дефис", `{"private-key": "BEGINKEY123"}`, "BEGINKEY123"},
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
		})
	}
}

// TestRedactTriggerIsCaseInsensitive — сравнение триггеров идёт по
// приведённой к нижнему регистру строке, поэтому форма в верхнем регистре
// обязана быть замечена. Правила присваивания и JSON помечены (?i), и
// «PASSWORD=…» раньше уходил в текст целиком.
func TestRedactTriggerIsCaseInsensitive(t *testing.T) {
	cases := []string{
		"PASSWORD=hunter2000",
		"Token=hunter2000",
		"Authorization: Bearer abcdefghijkl",
	}
	for _, in := range cases {
		if got := RedactSecrets(in); !strings.Contains(got, secretMask) {
			t.Errorf("не замаскировано: %q -> %q", in, got)
		}
	}
}
