package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Sandbox — граница файловой системы для одного запуска агента.
//
// Зачем. Без песочницы модель одной строкой читает «~/.gcli/config.json» с
// ключами API или «~/.ssh/id_rsa», и ничто этого не останавливает: пути от
// модели абсолютные, а «..» склеивается с рабочим каталогом. Песочница —
// единственное место, где такое ловится раз и навсегда, а не в каждом
// инструменте по отдельности.
//
// Границы задаются списком корней. Пустой список означает «песочницы нет»
// (режим по умолчанию у пользователя и во время работы над своим проектом):
// это осознанное решение, а не недосмотр — ломать привычное поведение
// никто не просил.
type Sandbox struct {
	// roots — абсолютные пути, внутри которых пути считаются допустимыми.
	roots []string
	// deny — абсолютные пути и поддеревья, к которым доступа нет даже внутри
	// песочницы. Сюда попадают каталоги с секретами.
	deny []string
	// enabled — песочница вообще включена.
	enabled bool
}

// NewSandbox — песочница на указанных корнях.
//
// Корни нормализуются и разворачиваются в абсолютные пути, потому что
// сравнение префиксов на «C:\gcli» и «C:\gclix» дало бы ложное срабатывание
// (классическая ошибка HasPrefix по путям).
func NewSandbox(roots ...string) *Sandbox {
	s := &Sandbox{enabled: true}
	for _, r := range roots {
		if a, err := filepath.Abs(r); err == nil {
			s.roots = append(s.roots, filepath.Clean(a))
		}
	}
	return s
}

// WithDeny — добавить запрещённые поддеревья.
func (s *Sandbox) WithDeny(paths ...string) *Sandbox {
	for _, p := range paths {
		if a, err := filepath.Abs(p); err == nil {
			s.deny = append(s.deny, filepath.Clean(a))
		}
	}
	return s
}

// Enabled — включена ли песочница.
func (s *Sandbox) Enabled() bool { return s != nil && s.enabled && len(s.roots) > 0 }

// Roots — разрешённые корни (для показа в UI и /permissions).
func (s *Sandbox) Roots() []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s.roots...)
}

// Deny — запрещённые пути.
func (s *Sandbox) Deny() []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s.deny...)
}

// Check — пропустить ли абсолютный путь.
//
// Порядок проверок важен: сперва корень, потом запрет. Иначе можно было бы
// «разрешить» путь, попав в deny, — нельзя.
func (s *Sandbox) Check(abs string) error {
	if !s.Enabled() {
		return nil
	}
	abs = filepath.Clean(abs)

	// Разыменовываем ВСЕГДА, а не только после неудачи буквальной проверки.
	// Иначе ссылка внутри разрешённого корня (junction «out» на каталог
	// пользователя) проходила бы как «путь внутри корня», а читался бы
	// совсем другой файл — и deny, и граница обходились за один шаг.
	real, err := resolveSymlinks(abs)
	if err != nil {
		return err
	}
	// Путь мог исчезнуть между разыменованием и проверкой — тогда прав на
	// него нет тем более.
	if real == "" {
		real = abs
	}
	if err := s.checkOne(real); err != nil {
		return err
	}
	// Буквальную проверку тоже делаем: файл мог ещё не существовать, и
	// разыменование тогда остановилось на предке, но имя внутри корня
	// должно быть допустимо само по себе.
	return s.checkOne(abs)
}

// checkOne — проверка одного (уже разобранного) пути без разыменования.
func (s *Sandbox) checkOne(abs string) error {
	// Запрет проверяем по всей цепочке компонентов пути, а не только по
	// префиксу: запрещённый каталог внутри разрешённого корня не должен
	// открываться обходом через «..» или через вложенный путь.
	for _, d := range s.deny {
		if within(abs, d) {
			return fmt.Errorf("песочница: доступ запрещён — %s", d)
		}
	}
	for _, root := range s.roots {
		if within(abs, root) {
			return nil
		}
	}
	return fmt.Errorf("песочница: путь вне рабочего каталога — %s (разрешено: %s)",
		abs, strings.Join(s.roots, ", "))
}

// resolveSymlinks — разыменовать симлинки в существующей части пути.
//
// os.Stat на «C:\gcli\link\config.json» уже показывает настоящий файл, но
// Check работает с записью, поэтому разыменовывать приходится вручную.
// EvalSymlinks требует, чтобы путь существовал целиком, а агент постоянно
// обращается к ещё не созданным файлам — поэтому берём самый глубокий
// существующий предок, разыменовываем его и приклеиваем остаток пути.
func resolveSymlinks(abs string) (string, error) {
	rest := ""
	cur := filepath.Clean(abs)
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return filepath.Clean(real), nil
			}
			return filepath.Join(real, rest), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// Дошли до корня — разыменовывать нечего.
			return filepath.Clean(abs), nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// within — лежит ли путь p внутри base или совпадает с ним.
func within(p, base string) bool {
	p, base = filepath.Clean(p), filepath.Clean(base)
	if p == base {
		return true
	}
	// Сравнение по компонентам, а не по строковому префиксу: иначе
	// «C:\gcli-secret» прошёл бы проверку внутри «C:\gcli».
	rel, err := filepath.Rel(base, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// DefaultDeny — пути с секретами, которые закрыты даже в песочнице.
//
// Это не паранойя, а страховка от «работаю в домашнем каталоге»: песочница
// на ~/src не должна превращаться в возможность прочитать ~/.ssh.
func DefaultDeny() []string {
	out := []string{}
	if h, err := os.UserHomeDir(); err == nil {
		out = append(out,
			filepath.Join(h, ".ssh"),
			filepath.Join(h, ".gnupg"),
			filepath.Join(h, ".aws"),
			filepath.Join(h, ".kube"),
			filepath.Join(h, ".docker"),
			filepath.Join(h, ".config", "gcloud"),
			filepath.Join(h, "AppData", "Roaming", "Mozilla"),
			filepath.Join(h, "AppData", "Roaming", "Chrome"),
		)
	}
	return out
}
