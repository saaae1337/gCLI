package tools

import (
	"testing"
)

// ---------- буква «ё» в автоподборе навыков ----------
//
// «ё» (U+0451) лежит вне диапазона «а»..«я» (U+0430..U+044F), и фильтр
// слов превращал её в разделитель: «учёт» распадалось на «уч» и «т»,
// оба короче трёх знаков и оба выбрасывались. Запрос «проверь учёт»
// набирал ноль очков против навыка с «учёт» в when — подбор молча
// проваливался, хотя слово совпадает целиком.

func TestSkillWordsKeepsYo(t *testing.T) {
	got := skillWords("учёт объём ёлка тест")
	want := map[string]bool{"учёт": false, "объём": false, "ёлка": false, "тест": false}
	for _, w := range got {
		if _, ok := want[w]; ok {
			want[w] = true
		}
	}
	for w, found := range want {
		if !found {
			t.Fatalf("слово «%s» потеряно; извлечено: %v", w, got)
		}
	}
}

func TestScoreSkillYoWordMatches(t *testing.T) {
	s := Skill{Name: "uchet", Desc: "навык про лимиты", When: "учёт лимитов, объём контекста"}
	sc := ScoreSkill(s, "проверь учёт")
	if sc.Score < skillMatchMinScore {
		t.Fatalf("«учёт» в query не сработал против when: score=%d, hits=%v", sc.Score, sc.Hits)
	}
}
