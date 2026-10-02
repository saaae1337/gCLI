package main

// Тесты интерфейса сессии для агента (app_session.go) и разбора ответа на
// подтверждение (readAns).
//
// Почему этот файл. Все функции app_session.go до сих пор имели 0% покрытия,
// а именно через них ход и субагенты дёргают состояние сессии параллельно:
// AddMessage, AddUsage, AddRequest, AddError. Каждая из них берёт sessMu
// не просто из вежливости — /v1/history и /v1/status читают историю из
// HTTP-горутин, пока идёт ход, и без блокировки это гонка чтения-записи на
// слайсе. Тест на -race с настоящими параллельными вызовами ловит снятие
// блокировки, а чтение кода — нет.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gcli/core"
	"gcli/tools"
)

// TestAddMessageAppends — сообщение попадает в историю в том же порядке.
func TestAddMessageAppends(t *testing.T) {
	a, _ := cmdApp(t)
	a.AddMessage(core.Message{Role: core.RoleUser, Content: "первый"})
	a.AddMessage(core.Message{Role: core.RoleAssistant, Content: "второй"})
	msgs := a.Messages()
	if len(msgs) != 2 {
		t.Fatalf("в истории %d сообщений, ждали 2", len(msgs))
	}
	if msgs[0].Content != "первый" || msgs[1].Content != "второй" {
		t.Errorf("порядок сообщений нарушен: %q / %q", msgs[0].Content, msgs[1].Content)
	}
}

// TestAddMessageConcurrentNoRace — главный тест файла: десятки параллельных
// AddMessage из разных горутин.
//
// Именно этот сценарий ломает гонкой: HTTP-клиент дергает /v1/history, пока
// агент пишет сообщения. Под -race потеря sessMu или снятие блокировки
// падают здесь, а не у пользователя в середине хода.
func TestAddMessageConcurrentNoRace(t *testing.T) {
	a, _ := cmdApp(t)
	const workers, each = 8, 50
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				a.AddMessage(core.Message{Role: core.RoleUser, Content: "сообщение"})
				// Параллельно читаем, как это делает /v1/history.
				_ = a.Messages()
				_ = a.Turns()
			}
		}(w)
	}
	wg.Wait()
	if got := len(a.Messages()); got != workers*each {
		t.Errorf("все сообщения должны сохраниться: %d из %d", got, workers*each)
	}
}

// TestReplaceMessagesSwapsHistory — сжатие истории подменяет её целиком.
func TestReplaceMessagesSwapsHistory(t *testing.T) {
	a, _ := cmdApp(t)
	a.AddMessage(core.Message{Role: core.RoleUser, Content: "было длинно"})
	a.ReplaceMessages([]core.Message{{Role: core.RoleUser, Content: "сжато"}})
	msgs := a.Messages()
	if len(msgs) != 1 || msgs[0].Content != "сжато" {
		t.Errorf("история не заменена: %+v", msgs)
	}
}

// TestReplaceMessagesAcceptsNil — пустая история допустима (сжатие может
// выкинуть всё), и это не должно падать на nil-слайсе в потребителях.
func TestReplaceMessagesAcceptsNil(t *testing.T) {
	a, _ := cmdApp(t)
	a.ReplaceMessages(nil)
	if got := len(a.Messages()); got != 0 {
		t.Errorf("история должна быть пустой, а не %d сообщениями", got)
	}
}

// TestAddUsageAccumulates — токены сходятся, а не перезаписываются.
//
// Ошибка здесь стоит денег: при перезаписи итог /usage показал бы токены
// только последнего запроса, и человек не увидел бы, во сколько обошёлся
// ход с двадцатью итерациями.
func TestAddUsageAccumulates(t *testing.T) {
	a, _ := cmdApp(t)
	a.AddUsage(core.Usage{PromptTokens: 100, CompletionTokens: 20})
	a.AddUsage(core.Usage{PromptTokens: 300, CompletionTokens: 30})
	if got := a.sess.Usage.PromptTokens; got != 400 {
		t.Errorf("входных токенов %d, ждали 400", got)
	}
	if got := a.sess.Usage.CompletionTokens; got != 50 {
		t.Errorf("выходных токенов %d, ждали 50", got)
	}
	if got := a.sess.Usage.Total(); got != 450 {
		t.Errorf("всего %d, ждали 450", got)
	}
}

// TestAddRequestRecordsBothUsageAndStats — один завершённый запрос попадает и
// в счётчик сессии, и в статистику.
//
// Эти два места считали по отдельности, и при расхождении /usage и /stats
// показывали разные числа об одном и том же ходе.
func TestAddRequestRecordsBothUsageAndStats(t *testing.T) {
	a, _ := cmdApp(t)
	a.AddRequest(core.Usage{PromptTokens: 500, CompletionTokens: 50}, 2*time.Second)
	if got := a.sess.Usage.Total(); got != 550 {
		t.Errorf("usage не учтён: %d", got)
	}
	if a.sess.Stats.Requests != 1 {
		t.Errorf("запросов учтено %d, ждали 1", a.sess.Stats.Requests)
	}
	if got := a.sess.Stats.TotalDuration; got != 2*time.Second {
		t.Errorf("время не учтено: %v", got)
	}
	if a.sess.Stats.LastPrompt != 500 {
		t.Errorf("последний запрос потерян: %d", a.sess.Stats.LastPrompt)
	}
}

// TestAddErrorCountsFailures — неудачные запросы видны в статистике.
//
// Без этого счётчика /stats рисовал бы стопроцентный успех, и человек
// искал бы, почему ответы приходят медленно, не зная, что половина
// запросов падала.
func TestAddErrorCountsFailures(t *testing.T) {
	a, _ := cmdApp(t)
	a.AddRequest(core.Usage{PromptTokens: 10, CompletionTokens: 1}, time.Second)
	a.AddError()
	a.AddError()
	if a.sess.Stats.Errors != 2 {
		t.Errorf("ошибок учтено %d, ждали 2", a.sess.Stats.Errors)
	}
	// Ошибка не должна подменять токены запроса.
	if got := a.sess.Usage.Total(); got != 11 {
		t.Errorf("ошибка испортила учёт токенов: %d", got)
	}
}

// TestUsageAndRequestsConcurrent — параллельный учёт расхода.
//
// Субагенты пишут usage из своих горутин (app_subagents.go зовёт AddUsage под
// sessMu), и параллельно с ними идёт основной ход. Без блокировки счётчик
// теряет токены — сумма сходится не с тем, что реально потрачено.
func TestUsageAndRequestsConcurrent(t *testing.T) {
	a, _ := cmdApp(t)
	const workers, each = 8, 25
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				a.AddUsage(core.Usage{PromptTokens: 10, CompletionTokens: 1})
				a.AddError()
				a.Turns()
			}
		}()
	}
	wg.Wait()
	if got := a.sess.Usage.PromptTokens; got != workers*each*10 {
		t.Errorf("потерян учёт: %d из %d", got, workers*each*10)
	}
	if a.sess.Stats.Errors != workers*each {
		t.Errorf("ошибок учтено %d из %d", a.sess.Stats.Errors, workers*each)
	}
}

// TestReadAnswers — все ответы разбираются как задумано.
//
// Особенно важна пара a/A: «A» (заглавная) — «все», «a» — «всегда». Раньше
// ToLower целиком делал заглавную A недостижимой веткой, и человек,
// выбравший «все», молча получал более слабое «всегда».
func TestReadAnswers(t *testing.T) {
	cases := []struct {
		line                  string
		allowAlways, allowAll bool
		want                  int
	}{
		{"y", true, true, confirmYes},
		{"д", true, true, confirmYes},
		{"да", true, true, confirmYes},
		{"yes", true, true, confirmYes},
		{"1", true, true, confirmYes},
		{"n", true, true, confirmNo},
		{"нет", true, true, confirmNo},
		{"0", true, true, confirmNo},
		{"", true, true, confirmNo}, // пустой Enter = нет
		{"a", true, true, confirmAlways},
		{"A", true, true, confirmAll}, // заглавная — «все»
		{"all", true, true, confirmAll},
		{"все", true, true, confirmAll},
		{"y", false, false, confirmYes},
		{"y", false, true, confirmYes},
		{" A ", true, true, confirmAll},
	}
	for _, c := range cases {
		a, _ := cmdApp(t)
		a.stdin = testStdin(c.line)
		if got := a.readAns(c.allowAlways, c.allowAll); got != c.want {
			t.Errorf("readAns(%q, %v, %v) = %d, ждали %d", c.line, c.allowAlways, c.allowAll, got, c.want)
		}
	}
}

// TestReadAnsRetriesOnGarbage — мусорный ответ не трактуется как «нет».
//
// Если бы мусор возвращал confirmNo, опечатка в «yes» тихо отменяла бы
// разрешение, и человек решил бы, что gcli сломался. Поэтому мусор
// переспрашивает.
func TestReadAnsRetriesOnGarbage(t *testing.T) {
	a, buf := cmdApp(t)
	a.stdin = testStdinMultiline("что-то", "y")
	if got := a.readAns(true, true); got != confirmYes {
		t.Errorf("после мусора ждали confirmYes, получили %d", got)
	}
	if !strings.Contains(out(buf), "ответь") {
		t.Errorf("нужно подсказать допустимые ответы: %q", out(buf))
	}
}

// TestReadAnsUnavailableAlwaysAsksAgain — «a» там, где его не предлагали,
// не превращается в молчаливое разрешение.
func TestReadAnsUnavailableAlwaysAsksAgain(t *testing.T) {
	a, buf := cmdApp(t)
	// Здесь «всегда» не предлагается: ответ «a» обязан быть отвергнут.
	a.stdin = testStdinMultiline("a", "n")
	if got := a.readAns(false, false); got != confirmNo {
		t.Errorf("ждали confirmNo, получили %d", got)
	}
	if !strings.Contains(out(buf), "недоступно") {
		t.Errorf("нужно сказать, что вариант недоступен: %q", out(buf))
	}
}

// TestReadAnsEofMeansNo — закрытый ввод = отказ, а не зависание.
//
// Это путь завершения REPL: если бы Scan() не вернул false, команда ждала бы
// ответа от несуществующего терминала. EOF — это ЗАКРЫТЫЙ канал: открытый
// пустой канал не даёт EOF, а честно блокирует Scan() (как и делает
// newStdin, который закрывает lines в defer при чтении os.Stdin).
func TestReadAnsEofMeansNo(t *testing.T) {
	a, _ := cmdApp(t)
	lines := make(chan string)
	close(lines)
	a.stdin = &stdinReader{lines: lines, keys: make(chan byte, 1)}
	if got := a.readAns(true, true); got != confirmNo {
		t.Errorf("закрытый ввод должен означать отказ, получено %d", got)
	}
}

// TestQuietConfirmSkipsUser — в машинном режиме (-p/--json) вопросов нет.
//
// Проверяем, что решение принимается по флагам, и что опасная команда под
// /yolo всё равно отвергается: -p не должен становиться «разрешить всё».
func TestQuietConfirmSkipsUser(t *testing.T) {
	cases := []struct {
		name string
		mut  func(a *app)
		// req собирается после создания приложения: путь записи берётся из
		// его workDir, а он известен только тогда.
		req  func(a *app) tools.ConfirmReq
		want bool
	}{
		{"обычная запись без флагов спрашивает", func(a *app) {}, func(a *app) tools.ConfirmReq { return execReq("go test ./...") }, false},
		{"yolo разрешает безопасное", func(a *app) { a.sess.Perms.BashAll = true }, func(a *app) tools.ConfirmReq { return execReq("go test ./...") }, true},
		{"yolo не разрешает опасное", func(a *app) { a.sess.Perms.BashAll = true }, func(a *app) tools.ConfirmReq { return execReq("rm -rf /") }, false},
		{"yolo разрешает запись в файл", func(a *app) { a.sess.Perms.BashAll = true }, func(a *app) tools.ConfirmReq { return writeReq(a.workDir) }, true},
		{"флаг файлов разрешает запись", func(a *app) { a.sess.Perms.FileWrite = true }, func(a *app) tools.ConfirmReq { return writeReq(a.workDir) }, true},
		{"флаг файлов не разрешает shell", func(a *app) { a.sess.Perms.FileWrite = true }, func(a *app) tools.ConfirmReq { return execReq("go build") }, false},
		{"автопилот разрешает безопасное", func(a *app) { a.sess.Perms.Autopilot = true }, func(a *app) tools.ConfirmReq { return execReq("go test ./...") }, true},
		{"автопилот спрашивает про опасное", func(a *app) { a.sess.Perms.Autopilot = true }, func(a *app) tools.ConfirmReq { return execReq("rm -rf /") }, false},
		// AutopilotAll всегда идёт парой с Autopilot (setAutopilotAll ставит
		// оба, как и флаг -autopilot-all в main.go), а quietConfirm смотрит
		// именно на Autopilot. Ставим оба — иначе тест проверял бы
		// несуществующее состояние, а не код.
		{"автопилот all разрешает опасное", func(a *app) {
			a.sess.Perms.AutopilotAll = true
			a.sess.Perms.Autopilot = true
		}, func(a *app) tools.ConfirmReq { return execReq("rm -rf /") }, true},
	}
	for _, c := range cases {
		a, _ := cmdApp(t)
		a.quiet = true
		c.mut(a)
		if got := a.quietConfirm(c.req(a)); got != c.want {
			t.Errorf("%s: quietConfirm = %v, ждали %v", c.name, got, c.want)
		}
	}
}

// TestAutoApproveDangerBoundary — ровно на границе опасности.
//
// Граница важна: команда, которая «выглядит безобидно», но удаляет, обязана
// спрашивать. Проверяем и опасный, и безопасный вариант, потому что ошибка
// в любую сторону одинаково дорога.
func TestAutoApproveDangerBoundary(t *testing.T) {
	a, _ := cmdApp(t)
	// Опасность определяет tools.IsDangerous, и граница там не «на глаз»:
	// rm -rf build не опасен (удаление каталога сборки — обычное дело),
	// а rm -rf / и rm -rf ~/x опасны. Тест берёт ровно эти значения, иначе
	// он проверял бы не код, а свою догадку об опасности.
	if a.autoApprove(execReq("rm -rf /")) {
		t.Error("rm -rf / должен спрашивать даже в автопилоте")
	}
	if a.autoApprove(execReq("rm -rf ~/tmp")) {
		t.Error("удаление в домашнем каталоге должно спрашивать")
	}
	if !a.autoApprove(execReq("rm -rf build")) {
		t.Error("rm -rf build — обычная чистка сборки, автопилот должен одобрить")
	}
	if !a.autoApprove(execReq("go test ./...")) {
		t.Error("go test в автопилоте одобряется")
	}
	// Запись файлов и сеть в автопилоте безопасны всегда.
	if !a.autoApprove(writeReq(a.workDir)) {
		t.Error("запись файла в автопилоте одобряется")
	}

	// AutopilotAll снимает границу опасности целиком — иначе «одобряет всё,
	// включая разрушительные» было бы неправдой.
	a.sess.Perms.AutopilotAll = true
	if !a.autoApprove(execReq("rm -rf /")) {
		t.Error("в режиме all rm -rf / должен одобряться — иначе all ничего не добавляет")
	}
}

// testStdinMultiline — stdinReader с несколькими готовыми строками.
func testStdinMultiline(lines ...string) *stdinReader {
	s := &stdinReader{lines: make(chan string, len(lines)), keys: make(chan byte, 1)}
	for _, l := range lines {
		s.lines <- l
	}
	return s
}

// writeTemp — файл с содержимым во временном каталоге.
func writeTemp(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// readFile — содержимое файла или падение теста.
func readFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("чтение %s: %v", p, err)
	}
	return string(data)
}

// TestRecordCheckpointBeforeEdit — перед правкой файла хранится копия.
//
// Это страховка /undo: без неё откат удалил бы файл, который человек
// писал руками, потому что агент его «изменил».
func TestRecordCheckpointBeforeEdit(t *testing.T) {
	a, _ := cmdApp(t)
	p := writeTemp(t, a.workDir, "config.txt", "старое")
	a.recordCheckpoint(p, "правка")
	if len(a.sess.Checkpoints) != 1 {
		t.Fatalf("чекпоинтов %d, ждали 1", len(a.sess.Checkpoints))
	}
	cp := a.sess.Checkpoints[0]
	if !cp.Existed {
		t.Error("существующий файл должен быть отмечен как существовавший")
	}
	data := readFile(t, cp.Backup)
	if data != "старое" {
		t.Errorf("копия не совпадает: %q", data)
	}
}

// TestRecordCheckpointForNewFile — нового файла на диске нет, но чекпоинт
// всё равно записан: иначе /undo не сможет его удалить.
func TestRecordCheckpointForNewFile(t *testing.T) {
	a, _ := cmdApp(t)
	p := filepath.Join(a.workDir, "новый.txt")
	a.recordCheckpoint(p, "создание")
	if len(a.sess.Checkpoints) != 1 {
		t.Fatalf("чекпоинтов %d, ждали 1", len(a.sess.Checkpoints))
	}
	if a.sess.Checkpoints[0].Existed {
		t.Error("несуществующий файл не должен быть отмечен как существовавший")
	}
}

// TestReadersOfSessionNoRace — регрессия на историю, которую читают не
// через Messages(), а напрямую.
//
// Так было в четырёх местах: selfReport отдавал живой слайц истории
// инструменту self_status, taskSummary и cmdCopy читали её без sessMu,
// а /v1/message брал последний ответ прямо из a.sess.Messages. Снаружи это
// выглядит безобидно — все эти читатели вызываются «после хода», — но
// self_status и taskSummary зовутся изнутри хода и из горутин субагентов,
// которые идут параллельно AddMessage. Тест держит запись и все четыре
// чтения одновременно: без sessMu в читателях он падает под -race.
func TestReadersOfSessionNoRace(t *testing.T) {
	a, _ := cmdApp(t)
	a.AddMessage(core.Message{Role: core.RoleUser, Content: "исходная задача"})
	a.sess.Title = "заголовок"

	const workers, each = 6, 40
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Писатель: то же, что делает ход агента.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < workers*each; i++ {
			a.AddMessage(core.Message{Role: core.RoleAssistant, Content: "ответ"})
		}
		close(stop)
	}()

	// Читатели — по одному на каждое исправленное место.
	readers := map[string]func(){
		"selfReport":   func() { _ = a.selfReport() },
		"taskSummary":  func() { _ = a.taskSummary() },
		"cmdCopy":      func() { a.cmdCopy() },
		"sessionSpent": func() { _ = a.sessionSpent() },
	}
	for name, fn := range readers {
		fn := fn
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					fn()
				}
			}
		}()
		_ = name
	}
	wg.Wait()

	if got := len(a.Messages()); got != workers*each+1 {
		t.Errorf("история потеряла сообщения: %d из %d", got, workers*each+1)
	}
	if a.taskSummary() == "" {
		t.Error("taskSummary обязан вернуть хоть что-то: заголовок сессии")
	}
}

// TestSelfReportHistoryIsCopy — снимок агента не должен отдавать живую
// историю наружу. Если History указывает на слайц сессии, то правка истории
// после вызова self_status изменит уже собранный отчёт — и модель увидит
// не то, что было на момент опроса.
func TestSelfReportHistoryIsCopy(t *testing.T) {
	a, _ := cmdApp(t)
	a.AddMessage(core.Message{Role: core.RoleUser, Content: "первое"})
	rep := a.selfReport()

	a.AddMessage(core.Message{Role: core.RoleUser, Content: "второе"})
	if len(rep.History) != 1 {
		t.Fatalf("в снимке должно быть состояние на момент вызова: %d сообщений", len(rep.History))
	}

	// И обратное направление: изменение снимка не трогает сессию.
	rep.History[0].Content = "подменено"
	if got := a.Messages()[0].Content; got != "первое" {
		t.Fatalf("правка снимка изменила сессию: %q", got)
	}
}
