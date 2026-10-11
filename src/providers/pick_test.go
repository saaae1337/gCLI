package providers

import (
	"testing"

	"gcli/core"
)

// ---------- выбор провайдера ----------

// TestPickPrefersReadyProvider — выбранный провайдер обязан быть
// готовым к работе, если готовый есть.
//
// Раньше перебор смотрел на p.Key != "", а не на HasKey(). Локальные
// модели — Ollama и LM Studio — ключа не имеют по определению
// (NoKey), и потому в перебор не попадали никогда. Человек с Ollama
// настроенным и без единого облачного ключа получал первого
// провайдера из списка, то есть zai без ключа, и gcli сообщал
// «нет ключа» при полностью рабочей локальной модели.
func TestPickPrefersReadyProvider(t *testing.T) {
	reg := &Registry{
		All: []*Provider{
			{ID: "zai", Kind: ProtoOpenAI, BaseURL: "https://api.z.ai", Key: ""},
			{ID: "ollama", Kind: ProtoOpenAI, BaseURL: "http://localhost:11434/v1", NoKey: true},
		},
		Config: core.Config{},
	}
	p := reg.Pick("")
	if !p.Ready() {
		t.Fatalf("выбран %q, который не готов к работе: ключа нет и базы нет", p.ID)
	}
	if p.ID != "ollama" {
		t.Errorf("выбран %q вместо готового ollama", p.ID)
	}
}

// TestPickReadyBeatsUnready — среди провайдеров с ключом выбирается
// тот, у кого ещё и база настроена.
func TestPickReadyBeatsUnready(t *testing.T) {
	reg := &Registry{
		All: []*Provider{
			{ID: "битый", Kind: ProtoOpenAI, BaseURL: "", Key: "ключ"},
			{ID: "целый", Kind: ProtoOpenAI, BaseURL: "https://api.example.com/v1", Key: "ключ"},
		},
		Config: core.Config{},
	}
	if p := reg.Pick(""); p.ID != "целый" {
		t.Errorf("выбран %q вместо готового", p.ID)
	}
}

// TestPickKeepsExplicitChoice — явно указанный провайдер берётся, даже
// если он не готов: человек сказал, и молча менять его выбор нельзя,
// иначе /provider и конфиг перестают значить.
func TestPickKeepsExplicitChoice(t *testing.T) {
	reg := &Registry{
		All: []*Provider{
			{ID: "ollama", Kind: ProtoOpenAI, BaseURL: "http://localhost:11434/v1", NoKey: true},
			{ID: "битый", Kind: ProtoOpenAI, BaseURL: "", Key: ""},
		},
		Config: core.Config{},
	}
	if p := reg.Pick("битый"); p.ID != "битый" {
		t.Errorf("явный выбор подменён на %q", p.ID)
	}
}

// TestPickFallsBackWhenNothingReady — когда готовых нет вовсе, вернуть
// надо что-то осмысленное, а не паниковать.
func TestPickFallsBackWhenNothingReady(t *testing.T) {
	reg := &Registry{All: []*Provider{}, Config: core.Config{}}
	if p := reg.Pick(""); p == nil {
		t.Fatal("Pick вернул nil вместо заглушки")
	}

	reg2 := &Registry{
		All:    []*Provider{{ID: "битый", Kind: ProtoOpenAI}},
		Config: core.Config{},
	}
	if p := reg2.Pick(""); p == nil {
		t.Fatal("Pick вернул nil при непустом списке")
	}
}

// TestReadyNeedsKeyAndBase — провайдер готов, когда есть и то, и другое.
func TestReadyNeedsKeyAndBase(t *testing.T) {
	cases := []struct {
		name string
		p    Provider
		want bool
	}{
		{"есть-ключ-и-база", Provider{Key: "k", BaseURL: "u"}, true},
		{"ключ-без-базы", Provider{Key: "k"}, false},
		{"база-без-ключа", Provider{BaseURL: "u"}, false},
		{"ключ-не-нужен", Provider{BaseURL: "u", NoKey: true}, true},
		{"ничего", Provider{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.p.Ready(); got != c.want {
				t.Errorf("Ready=%v, ждали %v", got, c.want)
			}
		})
	}
}
