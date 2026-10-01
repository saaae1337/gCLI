package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// loadCfgWith — загрузить конфиг из строки JSON.
func loadCfgWith(t *testing.T, body string) Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GCLI_HOME", home)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &Repo{Store: NewStore(), Cfg: DefaultConfig()}
	return *r.LoadConfig()
}

// TestExtendConfigNormalizesNegative — отрицательные лимиты продления
// схлопываются в ноль (то есть в дефолт пакета).
//
// Причина та же, что у бюджета: минус в конфиге — опечатка. Если пропустить
// его вниз, продление либо выродится в отказ при каждой попытке, либо шаг
// станет отрицательным и лимит начнёт уменьшаться на ходу.
func TestExtendConfigNormalizesNegative(t *testing.T) {
	cases := []struct {
		name                 string
		json                 string
		abs, extMax, extStep int
	}{
		{"не заданы", `{}`, 0, 0, 0},
		{"нормальные значения", `{"max_iters_abs": 300, "turn_extend_max": 3, "turn_extend_step": 25}`, 300, 3, 25},
		{"отрицательные", `{"max_iters_abs": -10, "turn_extend_max": -5, "turn_extend_step": -20}`, 0, 0, 0},
		{"нули", `{"max_iters_abs": 0, "turn_extend_max": 0, "turn_extend_step": 0}`, 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := loadCfgWith(t, c.json)
			if cfg.MaxItersAbs != c.abs {
				t.Errorf("MaxItersAbs = %d, ожидалось %d", cfg.MaxItersAbs, c.abs)
			}
			if cfg.TurnExtendMax != c.extMax {
				t.Errorf("TurnExtendMax = %d, ожидалось %d", cfg.TurnExtendMax, c.extMax)
			}
			if cfg.TurnExtendStep != c.extStep {
				t.Errorf("TurnExtendStep = %d, ожидалось %d", cfg.TurnExtendStep, c.extStep)
			}
		})
	}
}

// TestExtendConfigZeroMeansDefault — важное соглашение: ноль в конфиге означает
// «взять дефолт», а не «продление выключено». Если бы это было не так, пустой
// конфиг молча отключил бы механизм, и заметить это можно было бы только по
// обрыву работы на середине задачи.
func TestExtendConfigZeroMeansDefault(t *testing.T) {
	cfg := loadCfgWith(t, `{}`)
	if cfg.MaxItersAbs != 0 || cfg.TurnExtendMax != 0 || cfg.TurnExtendStep != 0 {
		t.Errorf("пустой конфиг должен оставлять нули: %+v", cfg)
	}
}

// TestExtendConfigRoundTrip — поля переживают сохранение и загрузку.
//
// Поле, которое не пишется в JSON, исчезает после первого же /setup, и человек
// обнаруживает это через неделю, когда лимит перестал продлеваться.
func TestExtendConfigRoundTrip(t *testing.T) {
	orig := DefaultConfig()
	orig.MaxItersAbs = 250
	orig.TurnExtendMax = 4
	orig.TurnExtendStep = 40

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	var back Config
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.MaxItersAbs != 250 || back.TurnExtendMax != 4 || back.TurnExtendStep != 40 {
		t.Errorf("поля продления не пережили JSON: %+v", back)
	}
}

// TestExtendConfigKeys — имена ключей в конфиге зафиксированы.
//
// Ключ переименуют незаметно: старый конфиг перестаёт читаться, значения
// молча возвращаются к дефолтам, и человек гадает, куда делось его 250.
func TestExtendConfigKeys(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxItersAbs = 250
	cfg.TurnExtendMax = 4
	cfg.TurnExtendStep = 40
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"max_iters_abs", "turn_extend_max", "turn_extend_step"} {
		if _, ok := m[key]; !ok {
			t.Errorf("ключ %q не появился в JSON конфига", key)
		}
	}
}
