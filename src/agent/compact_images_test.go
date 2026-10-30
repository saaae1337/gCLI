package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gcli/core"
	"gcli/providers"
)

// Компакция и картинки. Data картинки не переживает сохранение сессии
// (core.Image.Data — json:"-"): после resume в истории остаётся только
// Path. Запрос выжимки, собранный из сырых сообщений сессии, уходил с
// пустым base64 — провайдер отвечал 400, Compact молча возвращал false,
// и компакция этой сессии ломалась навсегда: каждый ход снова тащил
// полный контекст и снова падал.

// TestCompactFillsImagesAfterResume — запрос выжимки получает дозаполненные
// из файлов картинки, а старые снимки из выжимки выброшены; сама сессия
// при этом не загрязняется base64.
func TestCompactFillsImagesAfterResume(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(imgPath, []byte("PNGDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldShot := filepath.Join(dir, "old.png")
	if err := os.WriteFile(oldShot, []byte("OLDPNG"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := &plainSession{msgs: []core.Message{
		{Role: core.RoleUser, Content: "вот скрин ошибки",
			Images: []core.Image{{Path: oldShot, MIME: "image/png"}}},
		{Role: core.RoleAssistant, Content: "смотрю"},
		{Role: core.RoleUser, Content: "и ещё один",
			Images: []core.Image{{Path: imgPath, MIME: "image/png"}}},
		{Role: core.RoleAssistant, Content: "понял"},
	}}
	sp := &scriptProvider{steps: [][]string{textStep("Выжимка диалога.")}}
	a := New(Deps{
		Providers: providers.NewClient(),
		Provider:  sp.provider(t),
		Session:   sess,
		Model:     "тест",
	}, dir)
	if !a.Compact(context.Background(), false) {
		t.Fatal("Compact вернул false: выжимка не состоялась")
	}
	if len(sp.history) == 0 {
		t.Fatal("запрос к провайдеру не записан")
	}
	body := sp.history[0]
	if strings.Contains(body, `"data":""`) {
		t.Errorf("в запросе выжимки картинка с пустым base64 — провайдер ответит 400:\n%s", body)
	}
	if !strings.Contains(body, "UE5HREFUQQ==") { // base64("PNGDATA")
		t.Error("последний снимок не дозаполнен из файла")
	}
	if strings.Contains(body, "T0xEUE5H") { // base64("OLDPNG")
		t.Error("старый снимок тащится в выжимку — только лишние токены")
	}
	for _, m := range sess.Messages() {
		for _, img := range m.Images {
			if img.Data != "" {
				t.Error("base64 записан в сессию, а не только в запрос")
			}
		}
	}
}
