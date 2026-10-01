package core

// StripJSONC — убрать комментарии и висячие запятые, чтобы обычный
// encoding/json прочитал файл.
//
// Зачем: конфиг gcli должен правиться руками, а править JSON без
// комментариев невозможно — каждый второй вопрос «а что это делает?»
// остаётся без ответа в коде. OpenCode и VS Code используют JSONC, и
// пользователь приходит в gcli с этим ожиданием.
//
// Убираются три вида мусора: // до конца строки, /* … */ через строки
// и запятая прямо перед } или ]. Внутри строк ничего не трогается:
// содержимое вида ",}" — это данные, а не синтаксис.
func StripJSONC(src []byte) []byte {
	out := make([]byte, 0, len(src))
	inStr := false
	esc := false
	lineCmt := false
	blockCmt := false
	// Позиция запятой в out, которую ещё можно выбросить. Пока после
	// неё встретились только пробелы, переводы строк и комментарии, она
	// остаётся кандидатом: запятая прямо перед } или ] — висячая.
	pending := -1

	for i := 0; i < len(src); i++ {
		c := src[i]

		switch {
		case lineCmt:
			// Перевод строки сохраняем: он держит номера строк в
			// сообщениях об ошибке разбора.
			//
			// Кандидата на удаление он НЕ сбрасывает: комментарий между
			// запятой и скобкой — по-прежнему «между», как и перевод
			// строки. «"a", // сборка\n]» — висячая запятая, и её надо
			// снести, а «"a", // сборка\n "b"» — обычный разделитель,
			// и он уцелеет, потому что значение "b" сбросит кандидата.
			if c == '\n' {
				lineCmt = false
				out = append(out, c)
			}
			continue

		case blockCmt:
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				blockCmt = false
				i++
				// После закрытия комментария перевод строки уже был
				// скопирован (строка «*/» его не содержит), ничего
				// дописывать не нужно: иначе в вывод попал бы сам «*».
				continue
			}
			if c == '\n' {
				out = append(out, c)
			}
			continue

		case inStr:
			out = append(out, c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
				// Закрытая строка — это значение, запятая перед ней
				// разделительная, а не висячая.
				pending = -1
			}
			continue
		}

		switch {
		case c == '"':
			inStr = true
			pending = -1
			out = append(out, c)
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			lineCmt = true
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			blockCmt = true
			i++
		case c == ',':
			pending = len(out)
			out = append(out, c)
		case c == '}' || c == ']':
			if pending >= 0 {
				out = append(out[:pending], out[pending+1:]...)
			}
			pending = -1
			out = append(out, c)
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			// Пробелы и переводы строк кандидата не снимают: висячая
			// запятая может стоять в конце строки перед }.
			out = append(out, c)
		default:
			// Всё остальное — начало значения: число, true/false/null,
			// «{» или «[». После него запятая уже разделительная.
			pending = -1
			out = append(out, c)
		}
	}

	return out
}

// HasJSONC — есть ли в файле комментарии или висячие запятые.
//
// Нужен, чтобы не гонять StripJSONC на каждом config.json подряд:
// лишний проход по файлу с ключами — это лишняя работа без выгоды.
//
// Проверка честная, а не «есть ли подстрока //»: в значении вида
// "url": "https://example.com" двойной слэш есть всегда, и дешёвый
// вариант гонял бы StripJSONC на каждом файле. Здесь слэш считается
// только вне строк — тем же состоянием, что и в StripJSONC.
func HasJSONC(src []byte) bool {
	inStr := false
	esc := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			inStr = true
		case c == '/' && i+1 < len(src) && (src[i+1] == '/' || src[i+1] == '*'):
			return true
		case c == ',':
			// Запятая перед } или ] — висячая.
			for j := i + 1; j < len(src); j++ {
				switch src[j] {
				case ' ', '\t', '\r', '\n':
					continue
				case '}', ']':
					return true
				}
				break
			}
		}
	}
	return false
}
