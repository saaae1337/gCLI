package core

// Цепочка миссий: несколько последовательных автономных прогонов с
// переносом контекста.
//
// Зачем: extra-long-time (12 ч) — это много для одного прогона: бюджет
// кончается, контекст устаёт, человек спит. Цепочка дробит большую цель
// на шаги: каждый шаг — самостоятельный прогон со своим бюджетом, а
// итог предыдущего шага уходит в промпт следующего. Это тот же принцип,
// что у handoff, но на уровне дней, а не часов.
//
// Файл .gcli/mission_chain.json переживает перезапуск: после падения
// процессa /mission chain run продолжит с Index, а не с нуля.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// MissionChain — упорядоченный список шагов прогона.
type MissionChain struct {
	// Items — шаги цепочки. Каждый — полноценное задание миссии со
	// своими потолками: шаг без потолков превратил бы цепочку в
	// бесконечный прогон.
	Items []Mission `json:"items"`
	// Index — сколько шагов уже отработало (следующий стартует с Items[Index]).
	Index int `json:"index"`
}

// MissionChainPath — файл цепочки в проекте.
func MissionChainPath(workDir string) string {
	return filepath.Join(workDir, ".gcli", "mission_chain.json")
}

// LoadMissionChain — прочитать цепочку. ok=false, если файла нет.
func LoadMissionChain(workDir string) (*MissionChain, bool, error) {
	data, err := os.ReadFile(MissionChainPath(workDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var c MissionChain
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, false, fmt.Errorf("битый mission_chain.json: %v", err)
	}
	if c.Index < 0 {
		c.Index = 0
	}
	return &c, true, nil
}

// Save — записать цепочку атомарно.
func (c *MissionChain) Save(workDir string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(MissionChainPath(workDir), data, 0o644)
}

// Next — следующий неотработанный шаг.
func (c *MissionChain) Next() (Mission, bool) {
	if c.Index < 0 || c.Index >= len(c.Items) {
		return Mission{}, false
	}
	return c.Items[c.Index], true
}

// Done — все шаги отработали.
func (c *MissionChain) Done() bool { return c.Index >= len(c.Items) }

// Summary — человеческая строка прогресса.
func (c *MissionChain) Summary() string {
	return fmt.Sprintf("шаг %d из %d", c.Index, len(c.Items))
}
