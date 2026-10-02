package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ProjectConfig — конфиг уровня проекта (gcli.json).
//
// Имя файла выбрано как у конкурентов: opencode.json, .claude/settings.json.
// Человек, пришедший из другого агента, ищет «а где правила» — и находит
// знакомое имя, а не догадывается про config.json, который в gcli уже
// занят глобальными настройками с ключами API.
type ProjectConfig struct {
	Permissions *PermissionCfg `json:"permissions,omitempty"`
}

// LoadProjectConfig — прочитать gcli.json из каталога проекта.
//
// Поддерживается и gcli.json, и .gcli/gcli.json: второе место удобно тем,
// что держит всё проектное в одной скрытой папке рядом с настройками
// агентов (.gcli/agents, .gcli/skills).
//
// Файл необязателен: отсутствие — не ошибка, а нулевые правила.
func LoadProjectConfig(workDir string) (*ProjectConfig, string, error) {
	for _, name := range []string{
		filepath.Join(workDir, "gcli.json"),
		filepath.Join(workDir, "gcli.jsonc"),
		filepath.Join(workDir, ".gcli", "gcli.json"),
		filepath.Join(workDir, ".gcli", "config.json"),
	} {
		cfg, err := readProjectConfigFile(name)
		if err != nil {
			return nil, name, err
		}
		if cfg != nil {
			return cfg, name, nil
		}
	}
	return nil, "", nil
}

// readProjectConfigFile — nil, если файла нет; ошибка, если он есть, но
// испорчен. Молча пропускать битый конфиг нельзя: человек написал
// правило, agent его не увидит и объяснить, почему не увидел, не сможет.
func readProjectConfigFile(name string) (*ProjectConfig, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, nil
	}
	var cfg ProjectConfig
	if HasJSONC(data) {
		data = StripJSONC(data)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("конфиг проекта %s не разбирается: %w", name, err)
	}
	return &cfg, nil
}

// LayeredRules — собрать правила из всех слоёв конфигов.
//
// Слои от низшего приоритета к высшему:
//
//	gcli.json в проекте   — правила репозитория
//	.gcli/config.json     — то же, но в скрытой папке проекта
//	~/.gcli/config.json   — личный выбор пользователя
//
// Именно в этом порядке действует правило «последнее совпавшее выигрывает»:
// пользователь дописывает правило позже и перекрывает проектное. Обратный
// порядок отдал бы приоритет файлу из репозитория, который человек может
// даже не открывать, — а это передача контроля над агентом тому, кто
// прислал репозиторий.
func LayeredRules(workDir string, global *Config) (Rules, error) {
	pc, _, err := LoadProjectConfig(workDir)
	if err != nil {
		pc = nil
	}
	return LayeredRulesFrom(global, pc)
}

// LayeredRulesFrom — то же, но для уже прочитанных конфигов.
//
// Возвращает ошибку разбора правил отдельно от ошибки чтения конфига:
// сломанный gcli.json и сломанная строка правила внутри исправного файла —
// разные вещи, и человек должен знать, что именно чинить.
func LayeredRulesFrom(global *Config, project *ProjectConfig) (Rules, error) {
	var globalPerms, projectPerms *PermissionCfg
	if global != nil {
		globalPerms = global.Permissions
	}
	if project != nil {
		projectPerms = project.Permissions
	}
	// Порядок слоёв — от низшего приоритета к высшему, см. RuleSources.
	return RuleSources(projectPerms, globalPerms)
}

// PermissionSources — где искать правила, для подсказки в /permissions.
func PermissionSources(workDir string) []string {
	out := []string{filepath.Join(Home(), "config.json")}
	for _, name := range []string{"gcli.json", "gcli.jsonc", ".gcli/gcli.json", ".gcli/config.json"} {
		out = append(out, filepath.Join(workDir, name))
	}
	return out
}

// ProjectConfigTemplate — заготовка gcli.json для /permissions init.
//
// Пишется с комментариями: пустой файл бесполезен, а человек, который
// открыл его впервые, должен понять синтаксис без чтения документации.
const ProjectConfigTemplate = `{
  // Правила разрешений для этого проекта.
  //
  // Формат: "инструмент(шаблон)": "allow" | "ask" | "deny".
  // Последнее совпавшее правило выигрывает, поэтому частные правила
  // ставьте ниже общих.
  //
  // Инструменты: bash, edit (edit_file, write_file, multi_edit),
  // web_fetch, read_file, и другие имена из /tools.
  "permissions": {
    "rules": [
      // Чтение и запись внутри проекта — обычно без вопросов.
      "read_file: allow",
      "edit(*): allow",

      // Команды сборки и проверок безопасны.
      "bash(go build*): allow",
      "bash(go test*): allow",
      "bash(go vet*): allow",
      "bash(git status*): allow",
      "bash(git diff*): allow",
      "bash(git log*): allow",

      // Публикация и разрушительные команды — только с вопроса.
      "bash(git push*): ask",
      "bash(rm *): ask",

      // Сети наружу — всегда вопрос.
      "web_fetch: ask"
    ]
  }
}
`

// ProjectConfigDoc — gcli.json с заданными правилами.
//
// Формат тот же, что у ProjectConfigTemplate, но правила подставляются из
// того, что /init узнал о проекте. Шаблон и генерируемый файл обязаны
// совпадать по комментариям: человек читает сгенерированный gcli.json
// годами, и он должен объяснять синтаксис сам, а не отсылать к
// документации, которой у него нет.
func ProjectConfigDoc(rules []string) string {
	var b strings.Builder
	b.WriteString(`{
  // Правила разрешений для этого проекта. Сгенерировано /init.
  //
  // Формат: "инструмент(шаблон)": "allow" | "ask" | "deny".
  // Последнее совпавшее правило выигрывает, поэтому частные правила
  // ставьте ниже общих.
  //
  // Правила из этого файла не перебиваются ничем: ни согласием
  // "разрешил всё" из прошлого разговора, ни автопилотом, ни ключом -p.
  // Чтобы ослабить правило, отредактируй его здесь.
  //
  // Личные правила (~/.gcli/config.json) имеют приоритет выше проектных.
  "permissions": {
    "rules": [
`)
	for _, r := range rules {
		fmt.Fprintf(&b, "      %q,\n", r)
	}
	b.WriteString(`    ]
  }
}
`)
	return b.String()
}

// ExamplePermissionLines — примеры правил для /permissions help.
var ExamplePermissionLines = []string{
	"bash(git status*): allow",
	"edit(src/**): allow",
	"bash(git push*): ask",
	"bash(rm *): deny",
}
