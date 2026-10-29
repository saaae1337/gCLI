package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BOM от редакторов Windows не должен ломать front matter навыка:
// «\ufeff---» — это всё ещё начало front matter (ParseCustomAgent делает
// то же самое; без срезания весь блок name/description/when оставался
// бы в теле, а описание становилось первой строкой сырого «---»).
func TestParseSkillStripsBOM(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bom.md")
	content := "\ufeff---\nname: review-notes\ndescription: проверка правок\nwhen: когда про ревью\n---\n\nТело навыка.\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ParseSkill(p, "тест")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "review-notes" {
		t.Errorf("Name = %q, ожидалось %q — front matter потерян", s.Name, "review-notes")
	}
	if s.Desc != "проверка правок" {
		t.Errorf("Desc = %q, ожидалось %q", s.Desc, "проверка правок")
	}
	if s.When != "когда про ревью" {
		t.Errorf("When = %q, ожидалось %q", s.When, "когда про ревью")
	}
	if s.Body != "Тело навыка." {
		t.Errorf("Body = %q, ожидалось %q", s.Body, "Тело навыка.")
	}
	if strings.Contains(s.Body, "---") {
		t.Errorf("сырой front matter попал в тело: %q", s.Body)
	}
}
