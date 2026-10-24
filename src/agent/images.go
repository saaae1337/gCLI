package agent

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"

	"gcli/core"
)

// maxImageBytes — потолок на картинку, которую кладут в запрос.
const maxImageBytes = 4 << 20

// Картинка попадает в контекст один раз — когда модель на неё смотрит.
// Дальше она только дорогая: снимок экрана стоит полторы-две тысячи
// токенов, и десяток снимков в истории съедал половину окна на каждом
// запросе. Поэтому:
//
//   - в файле сессии картинка хранится только путём (base64 не пишется:
//     иначе сессия раздувалась до десятков мегабайт);
//   - в запрос картинка возвращается из файла;
//   - из истории в запрос уходят только последние картинки.

// fillImages — вернуть данные картинок в сообщения запроса.
//
// Путь известен: картинка лежит на диске (снимок экрана или файл,
// который пользователь попросил посмотреть). Если файла нет — картинка
// в запрос не попадает, и в её месте остаётся текстовая подпись.
func fillImages(msgs []core.Message) {
	for i := range msgs {
		if len(msgs[i].Images) == 0 {
			continue
		}
		// Копия списка: данные дописываются в запрос, а не в сессию.
		imgs := make([]core.Image, len(msgs[i].Images))
		copy(imgs, msgs[i].Images)
		for j := range imgs {
			img := &imgs[j]
			if img.Data != "" || img.Path == "" {
				continue
			}
			data, mime, ok := readImageFile(img.Path)
			if !ok {
				continue
			}
			img.Data = data
			if img.MIME == "" {
				img.MIME = mime
			}
		}
		msgs[i].Images = imgs
	}
}

// readImageFile — прочитать картинку с диска и закодировать в base64.
func readImageFile(path string) (data, mime string, ok bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	if len(raw) > maxImageBytes {
		raw = raw[:maxImageBytes]
	}
	return base64.StdEncoding.EncodeToString(raw), imageMIME(path), true
}

// imageMIME — тип картинки по расширению.
func imageMIME(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	}
	return "image/png"
}
