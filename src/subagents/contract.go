package subagents

import (
	"fmt"
	"sort"
	"strings"
)

// ---------- Контракт отчёта ----------
//
// Промпт типа требует отчёт в конкретных секциях («## Найдено», «## План»),
// но ничего не проверяет: модель волна выдать абзац вместо отчёта, и главный
// агент получает текст, из которого не видно, что вообще делалось и что
// осталось. Секции — самый дешёвый детерминированный контракт, который можно
// проверить без модели: это структура текста, а не смысл.
//
// Смысл проверяет заземление (см. grounding.go), а форма — здесь. Вместе они
// закрывают обе половины проблемы «субагент отчитался чем-то, но чем —
// непонятно».

// requiredSections — обязательные секции отчёта по типу субагента.
//
// Взяты из промптов в types.go и должны им соответствовать: контракт,
// которого нет в промпте, — это враньё проверяющего. Обновляя промпт,
// обновляй и здесь.
//
// Для general и custom контракта нет: у них нет предписанного формата, и
// требовать секции значило бы штрафовать нормальный свободный отчёт.
var requiredSections = map[Type][]string{
	TypeExplorer:   {"Найдено", "Вывод"},
	TypeReviewer:   {"Находки", "Итог"},
	TypePlanner:    {"Факты", "План"},
	TypeCoder:      {"Сделано", "Проверка"},
	TypeTester:     {"Запуск", "Добавлено"},
	TypeFrontend:   {"Сделано", "Проверено глазами"},
	TypeResearcher: {"Ответ", "Источники"},
	TypeDocs:       {"Изменено", "Проверено"},
}

// RequiredSections — контракт отчёта данного типа (копия, не общий срез).
func RequiredSections(t Type) []string {
	src := requiredSections[t]
	if len(src) == 0 {
		return nil
	}
	out := make([]string, len(src))
	copy(out, src)
	return out
}

// HasContract — задан ли контракт секций для типа.
func HasContract(t Type) bool { return len(requiredSections[t]) > 0 }

// SectionCheck — что с контрактом отчёта.
type SectionCheck struct {
	Required []string // чего требовали
	Missing  []string // чего нет
	Present  int      // сколько нашлось
}

// OK — контракт выполнен (либо контракта не было).
func (c SectionCheck) OK() bool { return len(c.Missing) == 0 }

// Text — объяснение для главного агента и для добивки.
func (c SectionCheck) Text() string {
	if len(c.Missing) == 0 {
		return ""
	}
	return fmt.Sprintf("в отчёте нет обязательных секций: %s", strings.Join(c.Missing, ", "))
}

// CheckSections — сверить отчёт с контрактом секций.
//
// Проверка по заголовкам, а не по вхождению подстроки: строка «- Проверка
// не выполнялась» не является секцией «## Проверка», и наоборот —
// «## Проверено глазами» не заменяет «## Проверка». Ошибки такого рода
// стоят дешевле ложного подтверждения.
func CheckSections(full string, t Type) SectionCheck {
	req := RequiredSections(t)
	ch := SectionCheck{Required: req}
	if len(req) == 0 {
		return ch // контракта нет — нечего нарушать
	}
	keys := sectionKeys(full)
	for _, r := range req {
		if hasSection(keys, r) {
			ch.Present++
		} else {
			ch.Missing = append(ch.Missing, r)
		}
	}
	return ch
}

// sectionKeys — нормализованные ключи всех заголовков отчёта.
func sectionKeys(full string) []string {
	var out []string
	for _, l := range strings.Split(full, "\n") {
		k, ok := sectionKey(l)
		if ok {
			out = append(out, k)
		}
	}
	return out
}

// sectionKey — заголовок markdown-раздела → нормализованный ключ.
func sectionKey(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "#") {
		return "", false
	}
	t = strings.TrimLeft(t, "#")
	t = strings.TrimSpace(t)
	if t == "" {
		return "", false
	}
	return normalizeSection(t), true
}

// normalizeSection — привести заголовок к сравнимому виду.
//
// Отбрасывается всё, что не относится к имени раздела: скобки с уточнением
// («## Факты (текущее состояние)»), двоеточия, маркеры списка, звёздочки
// жирного. Регистр и лишние пробелы значения не имеют.
func normalizeSection(s string) string {
	t := strings.ToLower(s)
	if i := strings.IndexAny(t, "(:"); i >= 0 {
		t = t[:i]
	}
	t = strings.Trim(t, " \t*•-—–:.")
	t = strings.Join(strings.Fields(t), " ")
	return t
}

// hasSection — есть ли в отчёте раздел с таким именем.
//
// Сравнение по префиксу, потому что модели любят уточнения: `## Итог по
// задаче` удовлетворяет контракту «Итог». Обратное («## Проверка» против
// контракта «Проверено глазами») не проходит — там разные слова.
func hasSection(keys []string, want string) bool {
	w := normalizeSection(want)
	if w == "" {
		return false
	}
	for _, k := range keys {
		if k == w || strings.HasPrefix(k, w+" ") || strings.HasPrefix(w, k+" ") {
			return true
		}
	}
	return false
}

// ---------- Вердикт по результату ----------

// JudgeReport — детерминированный вердикт «годен ли отчёт».
//
// Порядок проверок неслучаен: сначала форма (пустой, оборванный, мысль
// вслух), потом контракт секций, потом доказательства. Первая непрошедшая
// проверка и определяет вердикт — модели нельзя «договориться» с валидатором,
// выбрав удобную половину.
func JudgeReport(spec Spec, out Outcome) Quality {
	if q := AssessReport(out.Full); q != ReportOK {
		return q
	}
	if sc := CheckSections(out.Full, spec.Type); len(sc.Missing) > 0 {
		return ReportNoSections
	}
	if NeedsRepair(out.Audit) {
		return ReportUnverified
	}
	return ReportOK
}

// NeedsRepair — отчёт держится на недоказанных утверждениях.
//
// Не всякое непроверенное утверждение требует переделки: одна непрочитанная
// строка среди двадцати подтверждённых — обычная оговорка, и повтор всего
// запуска дороже её. Повтор оправдан, когда опора слабая (меньше половины
// ссылок подтверждено) или когда в отчёте есть фантомы — ссылка на несуществующий
// файл почти всегда означает выдумку, а не описку.
//
// Отдельно: если субагент не открыл ни одного файла, это не починка отчёта,
// а полноценное исследование с нуля — повтор тут бесполезен, и вердикт
// оставляет работу главному агенту.
func NeedsRepair(a *GroundingReport) bool {
	if a == nil || a.FilesRead == 0 {
		return false
	}
	if a.Checked == 0 && len(a.Phantoms) == 0 {
		return false // проверять нечего
	}
	return len(a.Phantoms) > 0 || a.Score() < 0.5
}

// GroundHint — добавка к задаче для повтора, когда отчёт не подтвердился.
//
// Адресная, а не общая: модели нужен список мест, а не слова «попробуй
// аккуратнее». Без списка повтор почти гарантированно воспроизводит тот же
// текст — ровно тот дефект, который этот слой и чинит.
func GroundHint(a *GroundingReport) string {
	if a == nil {
		return ""
	}
	// Сортируем ДО обрезки: список мест должен быть одинаковым от запуска к
	// запуску, иначе повтор обрезает разные дефекты и чинит то, что не сломалось.
	problems := sortedProblems(a)
	if len(problems) == 0 {
		return ""
	}
	if len(problems) > 12 {
		problems = problems[:12]
	}
	var b strings.Builder
	b.WriteString("\n[Система] Твой отчёт сверен с журналом инструментов, и он не подтвердился:\n")
	fmt.Fprintf(&b, "- прочитал ты %d файл(ов), ссылок проверено %d, подтверждено %d\n",
		a.FilesRead, a.Checked, a.Supported)
	b.WriteString("- проблемные места (открой их и подтверди или убери):\n")
	for _, p := range problems {
		b.WriteString("  - " + p + "\n")
	}
	b.WriteString("Перепиши отчёт заново. Не подтверждай то, чего не открывал, " +
		"и не выдумывай новых ссылок: отчёт без ссылок лучше отчёта с ложными.")
	return b.String()
}

// sortedProblems — список проблемных мест в стабильном порядке.
func sortedProblems(a *GroundingReport) []string {
	if a == nil {
		return nil
	}
	out := append([]string(nil), a.Problems()...)
	sort.Strings(out)
	return out
}
