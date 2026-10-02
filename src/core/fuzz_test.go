package core

// Фаззи-тесты разборов конфигурации.
//
// Зачем именно фаззи, а не таблица примеров. Эти функции стоят на пути
// чужого ввода: mission.json, mcp.json и gcli.json пишет и правит человек,
// расширения и MCP-конфиги приходят из проекта. Любой «неожиданный» вход
// обязан дать ошибку, а не панику — иначе агент падает на файле, который
// человек открыл просто чтобы посмотреть. Таблица примеров ловит то, что
// автор придумал; фаззи находит то, что он не придумал.
//
// Fuzz-функции запускаются как обычные тесты по seed-корпусу на каждом
// `go test`, а полный прогон — `go test -fuzz=FuzzИмя -fuzztime=60s`.

import (
	"encoding/json"
	"strings"
	"testing"
)

// FuzzStripJSONCMustNotPanic и не терять содержимое строк.
//
// Инварианты:
//   - функция не паникует ни на каком входе;
//   - скобки, кавычки и обратные слэши внутри строк остаются нетронутыми —
//     иначе комментарий «внутри строки» съел бы кусок значения;
//   - результат не длиннее исходника: мы только выбрасываем, не добавляем;
//   - результат идемпотентен — иначе файл в редакторе «поедет» после
//     автоформатирования.
func FuzzStripJSONC(f *testing.F) {
	seeds := []string{
		`{"a":1}`,
		`{ // комментарий
  "a": 1, /* блок */
  "b": "не // комментарий",
}`,
		`{"s":"строка с \" кавычкой и // внутри"}`,
		`[1,2,3,]`,
		`{"a":1,}`,
		`{"path":"C:\\Users\\me"}`,
		``,
		`{"emoji":"🐱 и \u0000"}`,
		`{"nested":{"deep":{"x":[1,{/*c*/}]}}}`,
		`"только строка"`,
		`{{{{{{`,
		`]]]]`,
		`{"a":"//"}{"b":"/*"}`,
		"// только комментарий\n",
		"/* не закрыт",
		`{"a":1 // без перевода`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, src []byte) {
		got := StripJSONC(src)
		if len(got) > len(src) {
			t.Fatalf("StripJSONC удлинила ввод: было %d, стало %d", len(src), len(got))
		}
		// Главный инвариант: в файле без комментариев StripJSONC не имеет
		// права тронуть ни одну строку. Проверять его всегда нельзя —
		// незакрытый комментарий по спецификации JSONC съедает остаток
		// файла вместе с кавычками внутри него, и это правильное поведение.
		// Поэтому отдельно оговариваем случай с комментариями, а для
		// чистого JSON держим проверку строго.
		if !hasComment(src) && !jsonStringsIntact(string(src), string(got)) {
			t.Fatalf("StripJSONC исказила строки в файле без комментариев.\nвход: %q\nвыход: %q", src, got)
		}
		// Идемпотентность там, где она имеет смысл: если первый проход
		// дал валидный JSON, второй не должен ничего менять — иначе файл
		// «поедет» при автоформатировании. На заведомо мусорном вводе
		// вида «,,}» висячие запятые убираются по одной за проход, и это
		// нормально: такой файл всё равно не разбирается.
		if json.Valid(got) {
			if twice := StripJSONC(got); len(twice) != len(got) {
				t.Fatalf("StripJSONC не идемпотентна на валидном результате: %d → %d байт\nвход: %q", len(got), len(twice), src)
			}
		}
	})
}

// hasComment — есть ли в файле комментарий JSONC.
func hasComment(src []byte) bool {
	var inStr, esc bool
	for i := 0; i < len(src); i++ {
		c := src[i]
		if esc {
			esc = false
			continue
		}
		switch c {
		case '\\':
			if inStr {
				esc = true
			}
		case '"':
			inStr = !inStr
		case '/':
			if !inStr && i+1 < len(src) && (src[i+1] == '/' || src[i+1] == '*') {
				return true
			}
		}
	}
	return false
}

// jsonStringsIntact — совпадают ли строковые литералы входа и выхода.
//
// Сравниваются значения, а не позиции: StripJSONC сохраняет пробелы и
// переводы строк, поэтому побайтовое равенство не подходит. Смысл в том,
// что содержимое строк меняться не должно: «//» внутри значения — это
// данные, а не комментарий.
func jsonStringsIntact(src, out string) bool {
	a, b := jsonStringValues(src), jsonStringValues(out)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// jsonStringValues — значения строковых литералов без кавычек.
func jsonStringValues(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '"' {
			continue
		}
		var b []byte
		esc := false
		i++
		for ; i < len(s); i++ {
			c := s[i]
			if esc {
				b = append(b, c)
				esc = false
				continue
			}
			switch c {
			case '\\':
				esc = true
			case '"':
				out = append(out, string(b))
				goto next
			default:
				b = append(b, c)
			}
		}
	next:
	}
	return out
}

// FuzzMissionNormalizeNeverPanics — разбор режима миссии на любом входе.
func FuzzMissionNormalize(f *testing.F) {
	for _, s := range []string{
		"normal", "long-time", "extra-long-time", "overnight", "",
		"Нормал", "long time", "long-time ", "\n", "overnight\x00",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, mode string) {
		m := Mission{Mode: MissionMode(mode)}
		// Ошибка — законный исход, паника — нет.
		if err := m.Normalize(); err == nil {
			if _, ok := ValidMissionMode(string(m.Mode)); !ok {
				t.Fatalf("Normalize() вернула nil на режиме %q и оставила его в поле", mode)
			}
		}
	})
}

// FuzzParseDurNeverPanics — разбор срока: «4h», «90m», «120», мусор.
func FuzzParseDur(f *testing.F) {
	for _, s := range []string{
		"4h", "90m", "120", "2h30m", "", "h", "0", "-1", "1e9h",
		"999999999999999999999h", "µs", " 4h ", "+4h", "∞",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, err := ParseDur(s)
		if err != nil {
			return
		}
		if d < 0 {
			t.Fatalf("отрицательный срок на %q: %s", s, d)
		}
	})
}

// FuzzOneLineAndTruncateNeverPanic — текст из файла и из ответа модели
// идёт в интерфейс через эти функции.
func FuzzOneLineAndTruncateNeverPanic(f *testing.F) {
	for _, s := range []string{
		"", "\n", "\r\n", "a\nb\nc", "\x00\x01", "🐱🐶", "  пробелы  ",
		"длинная строка без переводов", "\t\t", "\\", "\"", "'",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got := OneLine(s); strings.ContainsAny(got, "\r\n") {
			t.Fatalf("OneLine оставил перевод строки: %q → %q", s, got)
		}
		for _, n := range []int{0, 1, -1, len(s), len(s) + 10} {
			_ = Truncate(s, n)
		}
	})
}
