package tools

import (
        "os"
        "path/filepath"
        "regexp"
        "runtime"
        "strings"
)

// Проверка путей в shell-командах.
//
// Зачем. Песочница проверяет путь, когда его передали файловому инструменту:
// read_file/edit_file спросят Sandbox.Check и получат отказ. У bash другой
// путь — команда приходит строкой, и файловый инструмент по ней не проходит.
// Значит «cat ~/.ssh/id_rsa» или «echo x > ../outside.txt» выполнялись как
// обычные команды, а песочница молчала: включённая защита создавала ложное
// чувство безопасности.
//
// Границы решения, честно. Это проверка ПОВЕРХНОСТИ команды, а не системный
// вызов: мы разбираем текст до запуска. Команда может спрятать путь так, что
// регулярки его не увидят (закодированная строка, вычисление имени файла,
// чтение через дескриптор). Против такого нужен настоящий sandbox уровня ОС
// или контейнер. Здесь мы закрываем прямую и честную эксплуатацию — когда
// модель или пользователь пишет путь руками.

// tailPat — хвост шаблона пути: всё до пробела или символа, которым заканчивается
// аргумент оболочки.
const tailPat = `[^\s'"` + "`" + `|;&<>()]*(?:"[^"]*"|'[^']*')?`

var (
        // Windows-путь с буквой диска: C:\Users\me\.ssh\id_rsa, C:/Users/me.
        reWinAbs = regexp.MustCompile(`(?i)[a-z]:[\\/]` + tailPat)
        // Абсолютный unix/mingw-путь: /etc/passwd, /c/Users/me/notes.txt.
        // Начинаем с одного слэша, чтобы не ловить куски внутри URL.
        reAbs = regexp.MustCompile(`/` + tailPat)
        // Выход из рабочего каталога: ../x, ../../etc/passwd, ..\x.
        reRelUp = regexp.MustCompile(`\.\.[\\/]` + tailPat)
        // Домашний путь: ~/.ssh, ~\notes.txt, а также одиночная тильда.
        reHome = regexp.MustCompile(`~[\\/]?` + tailPat)
)

// sysPathOK — пути, которые не стоит считать нарушением песочницы.
//
// Это служебные каталоги: без них не работают перенаправления (2>/dev/null),
// временные файлы и запуск готовых программ. Секретов пользователя здесь нет,
// а домашний каталог (где они обычно лежат) в список не входит — для него
// работает deny.
var sysPathOK = []string{
        "/dev", "/proc", "/tmp", "/var/tmp", "/var/log",
        "/bin", "/sbin", "/usr/bin", "/usr/sbin", "/usr/local/bin",
}

// guardCommand — пропустить ли команду с учётом песочницы.
//
// Возвращает ошибку, если в тексте команды нашёлся путь, который песочница
// запрещает. Без песочницы (nil или выключенной) проверка не делается:
// пользовательский режим остаётся прежним.
func (r *Registry) guardCommand(cmd, workdir string) error {
        if !r.env.Sandbox.Enabled() {
                return nil
        }
        for _, p := range commandPaths(cmd, workdir) {
                if err := r.env.Sandbox.Check(p); err != nil {
                        return err
                }
        }
        return nil
}

// commandPaths — пути, встречающиеся в тексте команды, в абсолютном виде.
//
// Порядок разбора: сперва отсекаем URL (иначе «/v1/models» из
// «https://api/v1/models» сочтётся файлом), затем ищем известные формы
// путей. Каждый найденный кусок разрешается до абсолютного пути.
func commandPaths(cmd, workdir string) []string {
        var out []string
        add := func(p string) {
                if p == "" {
                        return
                }
                out = append(out, p)
        }
        home := userHome()

        // Домашний каталог разворачиваем ДО разбиения на токены: иначе «$HOME»
        // отделился бы от «/.ssh/id_rsa» разделителем, и путь потерялся бы.
        if home != "" {
                cmd = strings.ReplaceAll(cmd, "${HOME}", home)
                cmd = strings.ReplaceAll(cmd, "$HOME", home)
                cmd = strings.ReplaceAll(cmd, "%USERPROFILE%", home)
        }

        for _, tok := range splitCommandTokens(cmd) {
                // URL: в нём есть косая черта, но это не путь в файловой системе.
                if strings.Contains(tok, "://") {
                        continue
                }
                // Сам токен целиком: без этого «secret/token.txt» проверялся бы
                // только своим хвостом «/token.txt» и проскакивал мимо запрета.
                add(resolveCandidate(tok, workdir))
                // Плюс встроенные абсолютные пути: «--out=/etc/x», «cp a/b /tmp/c».
                for _, re := range []*regexp.Regexp{reWinAbs, reAbs, reRelUp, reHome} {
                        for _, loc := range re.FindAllStringIndex(tok, -1) {
                                // «/out» внутри «build/out» и «/...» внутри «./...» — это
                                // хвост относительного пути, а не самостоятельный путь.
                                // На Windows такой хвост абсолютивается мимо корня только
                                // по стечению обстоятельств, а на Linux filepath.IsAbs
                                // честно даёт «/out» — и нормальная команда блокируется.
                                // Хвост пропускаем: весь токен уже проверен целиком выше,
                                // а выход «../x» отдельно ловит reRelUp.
                                if re == reAbs && loc[0] > 0 && isWordByte(tok[loc[0]-1]) {
                                        continue
                                }
                                add(resolveCandidate(tok[loc[0]:loc[1]], workdir))
                        }
                }
        }
        return out
}

// isWordByte — символ, после которого «/x» — это хвост относительного пути
// («build/out», «./...»), а не начало абсолютного.
func isWordByte(b byte) bool {
        switch {
        case b >= '0' && b <= '9':
                return true
        case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z':
                return true
        case b == '_' || b == '.':
                return true
        }
        return false
}

// splitCommandTokens — разбить команду на куски по пробелам и разделителям.
//
// Разделители именно те, что имеют значение для оболочки: иначе «"rm -rf
// /tmp/x"» осталось бы одним токеном и путь внутри кавычек не нашёлся бы.
func splitCommandTokens(cmd string) []string {
        return strings.FieldsFunc(cmd, func(r rune) bool {
                switch r {
                case ' ', '\t', '\n', '\r', ';', '&', '|', '<', '>',
                        '(', ')', '"', '\'', '`', '=', ',', '*', '?', '[', ']', '{', '}', '$':
                        return true
                }
                return false
        })
}

// resolveCandidate — превратить найденный в команде кусок в абсолютный путь.
func resolveCandidate(p, workdir string) string {
        p = strings.Trim(p, `"'`)
        if p == "" {
                return ""
        }
        switch {
        case p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`):
                return filepath.Join(userHome(), strings.TrimLeft(p[1:], `/\`))
        case strings.HasPrefix(p, "~"):
                return filepath.Join(userHome(), strings.TrimLeft(p[1:], `/\`))
        case filepath.IsAbs(p) && runtime.GOOS == "windows" && len(p) > 2 && p[0] == '/':
                // MinGW-путь вида /c/Users/me: в Go это не абсолютный путь, поэтому
                // приводим к привычному C:\Users\me.
                if drv := p[1]; isDriveLetter(drv) {
                        return string(drv) + `:\` + filepath.FromSlash(strings.TrimLeft(p[2:], `/`))
                }
                return ""
        case posixSensitive(p):
                // Команда написана в POSIX-стиле («/etc/...»), а запускается,
                // возможно, в Git Bash, где такой путь уедет в каталог установки Git
                // или WSL, а не в проект. Разрешать его как «относительный от
                // проекта» нельзя — это была бы дыра по замыслу. Отдаём путь,
                // который песочница обязана отклонить.
                return filepath.FromSlash(p)
        case filepath.IsAbs(p):
                if sysAllowed(p) {
                        return ""
                }
                return filepath.Clean(p)
        case strings.HasPrefix(p, "../") || strings.HasPrefix(p, `..\`) ||
                strings.HasPrefix(p, "./") || strings.HasPrefix(p, `.\`):
                if abs, err := filepath.Abs(filepath.Join(workdir, filepath.FromSlash(p))); err == nil {
                        return abs
                }
                return ""
        case strings.ContainsAny(p, `/\`) && !looksLikeFlag(p):
                // Относительный путь внутри проекта (bin/app, src/tools/x.go).
                // Он и так внутри рабочего каталога, но проверку всё равно делаем:
                // так «../../secret» не проскочит.
                if abs, err := filepath.Abs(filepath.Join(workdir, filepath.FromSlash(p))); err == nil {
                        return abs
                }
                return ""
        }
        return ""
}

// looksLikeFlag — аргумент начинается с дефиса и это не путь вида -x/y.
func looksLikeFlag(p string) bool {
        return strings.HasPrefix(p, "-") && !strings.Contains(p, "/") && !strings.Contains(p, `\`)
}

// posixSensitive — путь, который на Windows указывает внутрь дерева Git
// или WSL, а не в проект.
//
// Список короткий и намеренно узкий: речь о путях, где цена ошибки высока
// (доступ к ключам), а обычную работу вида «go build ./...» он не задевает,
// потому что ./... начинается с точки и в этот список не попадает.
func posixSensitive(p string) bool {
        p = strings.TrimRight(strings.ReplaceAll(p, `\`, "/"), "/")
        if !strings.HasPrefix(p, "/") {
                return false
        }
        for _, sensitive := range []string{"/etc", "/root", "/home", "/Users", "/var/root", "/usr/local/etc"} {
                if p == sensitive || strings.HasPrefix(p, sensitive+"/") {
                        return true
                }
        }
        // Любой путь глубже двух уровней с корня — системный по умолчанию.
        // /tmp и /dev проверяются выше как служебные и сюда не доходят.
        if strings.Count(p, "/") >= 3 {
                return true
        }
        return false
}

// isDriveLetter — латинская буква диска в верхнем или нижнем регистре.
func isDriveLetter(c byte) bool {
        return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// sysAllowed — служебный путь, который песочница не контролирует.
func sysAllowed(p string) bool {
        p = strings.ReplaceAll(p, `\`, "/")
        for _, s := range sysPathOK {
                if p == s || strings.HasPrefix(p, s+"/") {
                        return true
                }
        }
        return false
}

// userHome — домашний каталог пользователя (пустой, если неизвестен).
// Имя не homeDir: в пакете есть тестовая homeDir(t) для подмены GCLI_HOME.
func userHome() string {
        h, err := os.UserHomeDir()
        if err != nil {
                return ""
        }
        return h
}
