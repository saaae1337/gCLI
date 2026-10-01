package core

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadConfigNormalizesBudget — отрицательный бюджет схлопывается в ноль.
//
// Отрицательное значение в конфиге — это опечатка, а не «бюджет, который уже
// перерасходован». Если пропустить его вниз, JudgeBudget решит, что остаток
// отрицательный, и маршрутизация задеплоит понижение у всех сразу и навсегда.
func TestLoadConfigNormalizesBudget(t *testing.T) {
	cases := []struct {
		name string
		json string
		want int
	}{
		{"не задан", `{}`, 0},
		{"нормальное значение", `{"sub_budget": 500000}`, 500000},
		{"отрицательное значение", `{"sub_budget": -1000}`, 0},
		{"ноль", `{"sub_budget": 0}`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("GCLI_HOME", home)
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(c.json), 0o600); err != nil {
				t.Fatal(err)
			}
			r := &Repo{Store: NewStore(), Cfg: DefaultConfig()}
			got := r.LoadConfig().SubBudget
			if got != c.want {
				t.Errorf("SubBudget = %d, ожидалось %d", got, c.want)
			}
		})
	}
}

// TestLoadConfigRoutingOffByDefault — маршрутизация выключена на пустом
// конфиге: включать её должен человек, а не первая же сессия.
func TestLoadConfigRoutingOffByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GCLI_HOME", home)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &Repo{Store: NewStore(), Cfg: DefaultConfig()}
	if cfg := r.LoadConfig(); cfg.SubRoute {
		t.Error("маршрутизация включена без явного указания в конфиге")
	}
}

// TestSubagentRecordSurvivesRoundTrip — объяснение выбора модели обязано дожить
// до файла сессии: /export и разбор прошлых запусков читают config/session с
// диска, а поле, которое туда не пишется, исчезает при первом же сохранении.
func TestSubagentRecordSurvivesRoundTrip(t *testing.T) {
	rec := SubagentRecord{
		ID:       "abc123",
		Name:     "explorer-1",
		Type:     "explorer",
		Model:    "claude-3-5-haiku",
		ModelWhy: "роль «explorer» простая, модель claude-opus-4 дороже",
		Status:   "done",
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var back SubagentRecord
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.ModelWhy != rec.ModelWhy {
		t.Errorf("ModelWhy = %q, ожидалось %q", back.ModelWhy, rec.ModelWhy)
	}
	if back.Model != rec.Model {
		t.Errorf("Model = %q, ожидалось %q", back.Model, rec.Model)
	}
}

// TestSubagentRecordWithoutWhyStaysCompact — обычный запуск, где маршрутизация
// не сработала, не должен раздувать каждую строку журнала пустой строкой.
func TestSubagentRecordWithoutWhyStaysCompact(t *testing.T) {
	data, err := json.Marshal(SubagentRecord{ID: "x", Name: "n"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("model_why")) {
		t.Errorf("пустое объяснение попало в JSON: %s", data)
	}
}
