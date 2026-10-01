package tools

import (
	"fmt"
	"sort"
	"strings"
)

// ---------- Автоподбор навыка по описанию задачи ----------

// Симптом, который закрывает MatchSkill. В системном промпте лежит список
// навыков, но модель регулярно зовёт load_skill с несуществующим именем —
// потому что помнит «навык про ревью», а не «code-review». Раньше такой вызов
// заканчивался ошибкой со списком имён, и ход тратился впустую. Теперь
// достаточно передать query — что агент делает, — и навык подберётся сам.

const (
	// skillMatchMinScore — порог совпадения. Ниже него «навык» выбирался бы
	// почти всегда: любое общее слово вроде «файлы» есть в половине описаний.
	skillMatchMinScore = 2
	// skillMatchMax — сколько кандидатов показывать в подсказке.
	skillMatchMax = 3
)

// skillStopWords — слова, которые не различают навыки между собой.
//
// Русские и английские стоп-слова: в запросе «проверь правки в коде» слово
// «в» или «the» не должно поднимать оценку ни одному навыку.
var skillStopWords = map[string]bool{
	"и": true, "в": true, "во": true, "на": true, "с": true, "к": true,
	"по": true, "для": true, "из": true, "от": true, "до": true, "не": true,
	"что": true, "как": true, "где": true, "но": true, "а": true, "или": true,
	"если": true, "уже": true, "ещё": true, "еще": true, "при": true,
	"под": true, "над": true, "про": true, "без": true, "её": true,
	"мне": true, "меня": true, "мой": true, "моя": true, "мои": true,
	"нужно": true, "надо": true, "можешь": true, "найди": true, "сделай": true,
	"the": true, "a": true, "an": true, "of": true, "to": true, "in": true,
	"on": true, "for": true, "and": true, "or": true, "is": true, "be": true,
	"with": true, "that": true, "this": true, "it": true, "as": true, "at": true,
}

// skillWords — разобрать текст на значимые слова (нижний регистр, без пунктуации).
func skillWords(s string) []string {
	low := strings.ToLower(s)
	// Пунктуация и переносы строк — разделители слов, но дефис в
	// «data-race» важен: заменяем на пробел, теряя только склейку.
	low = strings.Map(func(r rune) rune {
		if r == '-' || r == '_' {
			return ' '
		}
		if r >= 'а' && r <= 'я' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return ' '
	}, low)
	var out []string
	for _, w := range strings.Fields(low) {
		if len(w) < 3 || skillStopWords[w] {
			continue
		}
		out = append(out, w)
	}
	return out
}

// SkillScore — насколько навык подходит под запрос.
type SkillScore struct {
	Skill Skill
	Score int
	Hits  []string // совпавшие слова — показываем, чтобы выбор был объясним
}

// ScoreSkill — оценить навык по запросу.
//
// Вес слова зависит от поля: попадание в имя — сильный сигнал («ревью» →
// code-review), в when — средний, в описание — слабый. Без разницы весов
// навык с самым длинным описанием выигрывал бы у узкого, но точного.
func ScoreSkill(s Skill, query string) SkillScore {
	words := skillWords(query)
	if len(words) == 0 {
		return SkillScore{Skill: s}
	}
	name := " " + strings.ToLower(s.Name) + " "
	when := strings.ToLower(s.When)
	desc := strings.ToLower(s.Desc)
	res := SkillScore{Skill: s}
	seen := map[string]bool{}
	for _, w := range words {
		if seen[w] {
			continue
		}
		switch {
		case strings.Contains(name, w):
			res.Score += 4
			seen[w] = true
		case strings.Contains(when, w):
			res.Score += 3
			seen[w] = true
		case strings.Contains(desc, w):
			res.Score += 2
			seen[w] = true
		}
		if seen[w] {
			res.Hits = append(res.Hits, w)
		}
	}
	return res
}

// RankSkills — отсортировать навыки по совпадению с запросу (по убыванию).
//
// Сортировка стабильная по имени: при равных оценках порядок не должен
// зависеть от порядка каталога, иначе один и тот же запрос давал бы
// разные навыки в разных запусках.
func RankSkills(skills []Skill, query string) []SkillScore {
	out := make([]SkillScore, 0, len(skills))
	for _, s := range skills {
		out = append(out, ScoreSkill(s, query))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Skill.Name < out[j].Skill.Name
	})
	return out
}

// MatchSkill — подобрать навык под запрос.
func (r *Registry) MatchSkill(query string) (Skill, error) {
	if strings.TrimSpace(query) == "" {
		return Skill{}, fmt.Errorf("пустой query — опиши, что ты делаешь, или укажи name навыка")
	}
	enabled := make([]Skill, 0, 16)
	for _, s := range r.LoadSkills() {
		if !r.SkillOff(s.Name) {
			enabled = append(enabled, s)
		}
	}
	if len(enabled) == 0 {
		return Skill{}, fmt.Errorf("навыки не настроены — добавь markdown в .gcli/skills или ~/.gcli/skills")
	}
	ranked := RankSkills(enabled, query)
	if ranked[0].Score < skillMatchMinScore {
		names := r.SkillNames()
		hint := ""
		if len(names) > 0 {
			hint = " доступные: " + strings.Join(names, ", ")
		}
		return Skill{}, fmt.Errorf("по запросу %q навык не подобрался%s — укажи name точно или переформулируй", coreOneLine(query), hint)
	}
	return ranked[0].Skill, nil
}

// MatchSkillTop — подобрать навык и показать альтернативы.
//
// Нужен в подсказке: когда навык найден, модель не знает, что существовал
// ещё один подходящий, и в спорной ситуации выберет наугад.
func (r *Registry) MatchSkillTop(query string) (Skill, []Skill, error) {
	enabled := make([]Skill, 0, 16)
	for _, s := range r.LoadSkills() {
		if !r.SkillOff(s.Name) {
			enabled = append(enabled, s)
		}
	}
	ranked := RankSkills(enabled, query)
	if len(ranked) == 0 || ranked[0].Score < skillMatchMinScore {
		return Skill{}, nil, fmt.Errorf("по запросу %q навык не подобрался", coreOneLine(query))
	}
	var alts []Skill
	for i := 1; i < len(ranked) && len(alts) < skillMatchMax-1; i++ {
		// Порог — половина лучшей оценки, а не просто минимум: иначе в
		// «похожих» попадал бы любой навык, зацепивший одно общее слово
		// («код», «файл»), и подсказка сама стала бы шумом. На равных
		// оценках альтернатива остаётся: выбор всё равно за моделью.
		if ranked[i].Score < skillMatchMinScore || ranked[i].Score*2 < ranked[0].Score {
			continue
		}
		alts = append(alts, ranked[i].Skill)
	}
	return ranked[0].Skill, alts, nil
}
