package tools

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gcli/core"
)

// ---------- dry_run ----------
//
// Проверка инструмента обычно требует временного файла-драйвера: пишешь
// тест, запускаешь, смотришь вывод, удаляешь. Три вызова и мусор в проекте
// на ровно то время, пока идёт проверка.
//
// dry_run решает это один раз и навсегда: показывает, что инструмент
// СДЕЛАЛ БЫ, ничего не делая. Для проверки самого инструмента этого
// достаточно, а главное — не нужно трогать проект.

// maxReadBytes — потолок размера файла для чтения целиком.
//
// Зачем. os.ReadFile на многогигабайтном логе съедает память агента и
// контекст целиком, а модель всё равно не прочитает 4 ГБ. Раньше read_file
// читал файл целиком, разбивал на строки и только потом усекал до limit —
// то есть платил полную памятью за то, что выбросит. Проверяем размер ДО
// чтения, а для больших файлов читаем начало файла и честно говорим, что
// он обрезан: агент может попросить кусок через offset.
const maxReadBytes = 4 << 20 // 4 МБ

// maxInspectBytes — потолок для inspect: он показывает файл целиком в
// escape-виде, поэтому потолок ниже.
const maxInspectBytes = 512 << 10 // 512 КБ

// readFileLimited — прочитать файл с учётом потолка размера.
//
// Возвращает (данные, total, обрезан, ошибка): вызывающему нужно знать не
// только что показать, но и что файл больше показанного.
func readFileLimited(p string, limit int64) (data []byte, total int64, truncated bool, err error) {
	if limit <= 0 {
		limit = maxReadBytes
	}
	if st, statErr := os.Stat(p); statErr == nil {
		if st.IsDir() {
			return nil, 0, false, fmt.Errorf("это каталог: %s", p)
		}
		total = st.Size()
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, total, false, err
	}
	defer f.Close()

	// Читаем limit+1 байт: лишний байт — признак «файл длиннее потолка».
	data, err = io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, total, false, err
	}
	if int64(len(data)) > limit {
		return data[:limit], total, true, nil
	}
	return data, total, false, nil
}

// readLinesFrom — прочитать файл начиная со строки skip (0-based).
//
// Зачем не «прочитал целиком и разрезал»: при offset=50000 файл в 4 ГБ
// пришлось бы сначала целиком затянуть в память, чтобы отдать одну строку.
// Читаем потоком, пропуская ненужное, и уходим с диска сразу после нужного
// куска.
//
// linesLimit — потолок строк в ответе; bytesLimit — потолок байт, чтобы
// строка в 10 МБ (минифицированный js, base64 в одной строке) не съела память.
func readLinesFrom(p string, skip, linesLimit int, bytesLimit int64) (data []byte, size int64, skippedToEOF bool, err error) {
	if linesLimit <= 0 {
		linesLimit = 2000
	}
	if bytesLimit <= 0 {
		bytesLimit = maxReadBytes
	}
	if st, statErr := os.Stat(p); statErr == nil {
		if st.IsDir() {
			return nil, 0, false, fmt.Errorf("это каталог: %s", p)
		}
		size = st.Size()
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, size, false, err
	}
	defer f.Close()

	br := bufio.NewReaderSize(f, 64*1024)
	// Потолок на прочитанное: offset сам по себе уже ограничивает обход,
	// но limit может быть огромным, а offset — нулевым.
	maxRead := bytesLimit
	if int64(skip) > 0 {
		maxRead += int64(skip) * 256 // грубая оценка «строка тем длиннее, чем дальше»
	}

	var buf bytes.Buffer
	read := 0
	line := 0
	kept := 0
	for {
		if int64(read) >= maxRead {
			return buf.Bytes(), size, false, nil
		}
		b, err := br.ReadSlice('\n')
		read += len(b)
		if err == nil {
			// Строка целиком в буфере. Счётчик line — это номер строки
			// (1-based), поэтому «нужна» она при line > skip.
			line++
			if line > skip && kept < linesLimit {
				buf.Write(b)
				kept++
			}
			// Собрали запрошенное число строк — файл дальше можно не читать.
			// atEOF=false: файл-то не кончился, мы просто остановились.
			if kept >= linesLimit {
				return buf.Bytes(), size, false, nil
			}
			continue
		}
		if err == bufio.ErrBufferFull {
			// Строка длиннее буфера: это хвост одной и той же строки,
			// а не отдельная строка. Пишем кусок и ждём завершения —
			// иначе каждая порция по 64К учитывалась как целая строка,
			// и лимит «2000 строк» кончался на сотне строк минифицированного
			// JS (после фикса — считает одну длинную строку один раз).
			if line >= skip {
				buf.Write(b)
			}
			continue
		}
		if err == io.EOF {
			if len(b) > 0 {
				line++
				if line > skip && kept < linesLimit {
					buf.Write(b)
					kept++
				}
			}
			// Файл кончился. Если последняя строка попала в выборку —
			// это честный конец файла; если не попала, значит offset
			// ушёл за пределы и читать дальше нечего.
			return buf.Bytes(), size, line <= skip, nil
		}
		return nil, size, false, err
	}
}

// readFileSafe — прочитать файл с внятной ошибкой вместо системной.
func readFileSafe(p string) ([]byte, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("файл не найден: %s", p)
		}
		return nil, err
	}
	return data, nil
}

// ---------- След заданных вопросов ----------
//
// ask_user в автопилоте и в конвейерном режиме выглядит одинаково: модель
// ждёт ответа и не знает, был ли он вообще. Один вызов, который повис в
// очереди, стоит целого хода.

// askItem — один заданный вопрос.
type askItem struct {
	At       time.Time
	Question string
	Answer   string
	// Pending — вопрос задан, но ответа так и не пришло.
	Pending bool
}

// askLog — журнал вопросов реестра.
//
// Не глобальный: у gcli одновременно живут главный агент и пул субагентов, и
// общий журнал смешивал бы их вопросы. Агент, задавший вопрос, получал бы в
// ask_trace чужие ответы и решал бы задачу по чужому контексту.
type askLog struct {
	mu    sync.Mutex
	items []askItem
}

func (a *askLog) add(q, ans string, pending bool) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.items = append(a.items, askItem{At: time.Now(), Question: q, Answer: ans, Pending: pending})
	// Журнал не должен расти бесконечно: держим последние вопросы хода.
	if len(a.items) > 20 {
		a.items = a.items[len(a.items)-20:]
	}
}

func (a *askLog) all() []askItem {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]askItem, len(a.items))
	copy(out, a.items)
	return out
}

// recordAsk — вызывается из hAsk: помечает, что вопрос задан.
func (r *Registry) recordAsk(q, ans string, pending bool) {
	if r.asks == nil {
		r.asks = &askLog{}
	}
	r.asks.add(q, ans, pending)
}

// hAskTrace — что я спрашивал у пользователю и что получил в ответ.
func (r *Registry) hAskTrace(_ context.Context, _ map[string]any) (Result, error) {
	items := r.asks.all()

	if len(items) == 0 {
		return Result{
			Text:    "Вопросов пользователю в этом ходе не было. Если ты поставил задачу в тупик и ждёшь ответа — такого следа нет: значит, вопрос не был задан, и продолжай сам или спроси явно.",
			Summary: "вопросов не было",
		}, nil
	}
	var b strings.Builder
	b.WriteString("# Вопросы пользователю\n\n")
	pending := 0
	for i, it := range items {
		if it.Pending {
			pending++
			fmt.Fprintf(&b, "%d. ⏳ %s\n   — ответа нет: вопрос задан, но пользователь не ответил (ask_user вернул ошибку).\n",
				i+1, core.Truncate(core.OneLine(it.Question), 200))
			continue
		}
		fmt.Fprintf(&b, "%d. %s\n   → %s\n", i+1,
			core.Truncate(core.OneLine(it.Question), 200),
			core.Truncate(core.OneLine(it.Answer), 200))
	}
	if pending > 0 {
		fmt.Fprintf(&b, "\n⏳ Вопросов без ответа: %d. Ответ не придёт сам — либо продолжай без него,\n"+
			"либо прими решение сам и скажи об этом вслух в отчёте.\n", pending)
	}
	return Result{Text: strings.TrimSpace(b.String()), Summary: fmt.Sprintf("вопросов: %d", len(items))}, nil
}

// ---------- Фоновые процессы ----------

// Фоновые процессы нужны для долгих дел: сборка, тесты, деплой. Синхронный
// bash упирается в таймаут 600 с и рвёт команду на полуслове — вывод теряется
// вместе с кодом выхода. Фоновый режим отдаёт дескриптор, а вывод забирается
// потом.

// jobState — состояние фоновой задачи.
type jobState string

const (
	jobRunning jobState = "выполняется"
	jobDone    jobState = "завершилась успешно"
	jobFailed  jobState = "завершилась с ошибкой"
	jobStopped jobState = "остановлена"
	// jobUnknown — код выхода получить не удалось: процесс убит сигналом либо
	// завершился не штатно. Отличать от jobFailed нужно, чтобы не выдавать
	// «тесты упали» там, где на самом деле «тесты не дошли до конца».
	jobUnknown jobState = "завершилась, код выхода неизвестен"
)

// Job — фоновая задача.
type Job struct {
	ID      string
	Command string
	Dir     string
	Started time.Time
	// Путь к файлу с выводом: он переживает ход, в отличие от памяти.
	OutPath string
	// ExitCode — код завершения. -1 пока процесс ещё идёт или код неизвестен.
	ExitCode int
	// FinishedAt — момент завершения (zero, пока процесс работает).
	FinishedAt time.Time
	// Err — текст ошибки запуска, если процесс не стартовал.
	Err string
	// Stopped — задачу остановили вручную.
	Stopped bool

	mu sync.Mutex
	// stop — убить процесс. Отдельное поле, а не поле Job целиком: иначе
	// обращение к нему требовало бы блокировки, которую держит соседний метод.
	stop func()
}

// Status — текущее состояние задачи (потокобезопасно).
func (j *Job) Status() jobState {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.statusLocked()
}

// statusLocked — Status без захвата блокировки. Вызывается только под j.mu.
func (j *Job) statusLocked() jobState {
	if j.Stopped {
		return jobStopped
	}
	if j.FinishedAt.IsZero() {
		return jobRunning
	}
	switch {
	case j.ExitCode == 0:
		return jobDone
	case j.ExitCode < 0:
		return jobUnknown
	default:
		return jobFailed
	}
}

// Done — завершилась ли задача.
//
// Отдельный метод, а не Status() != jobRunning: вызывающий код держит j.mu,
// а Status() захватывает его повторно — sync.Mutex не переentrant, и такое
// приводило к зависанию всего агента намертво.
func (j *Job) Done() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.statusLocked() != jobRunning
}

// Exit — код завершения для отображения.
func (j *Job) Exit() (int, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.FinishedAt.IsZero() {
		return 0, false
	}
	return j.ExitCode, true
}

// jobRegistry — реестр фоновых задач.
//
// Глобальный по той же причине, что и счётчик итераций у агента: фоновые
// процессы переживают отдельные вызовы инструмента, и привязать их к
// экземпляру реестра нельзя — реестр пересоздаётся при каждом ходе.
var jobRegistry = struct {
	mu   sync.Mutex
	jobs map[string]*Job
	seq  int
}{jobs: map[string]*Job{}}

// maxJobs — сколько задач держим в реестре.
//
// Без ограничения список за пару часов работы превращался в свалку из
// двадцати завершённых процессов, и полезный сигнал «что сейчас работает»
// тонул в мусоре.
const maxJobs = 20

// jobLogDir — каталог с логами фоновых задач.
func jobLogDir() string { return filepath.Join(core.Home(), "jobs") }

// startJob — запустить команду в фоне.
//
// timeout <= 0 означает «без ограничения»: для фоновой задачи это и есть
// смысл, иначе она отличалась бы от bash только задержкой вывода.
func startJob(cmd, dir string, timeout int) *Job {
	if dir == "" {
		dir = "."
	}
	logDir := jobLogDir()
	_ = os.MkdirAll(logDir, 0o755)

	// ID считаем под блокировкой: UnixNano недостаточен — два запуска в одну
	// миллисекунду дали бы одинаковый ID и один из процессов стал бы
	// недоступен навсегда.
	jobRegistry.mu.Lock()
	jobRegistry.seq++
	id := fmt.Sprintf("job%d", jobRegistry.seq)
	outPath := filepath.Join(logDir, id+".log")
	jobRegistry.mu.Unlock()

	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		j := &Job{ID: id, Command: cmd, Dir: dir, Started: time.Now(), OutPath: "", ExitCode: -1, Err: err.Error(), FinishedAt: time.Now()}
		registerJob(j)
		return j
	}
	_, _ = f.WriteString(fmt.Sprintf("[запущено %s, команда: %s]\n", time.Now().Format("15:04:05"), cmd))

	j := &Job{ID: id, Command: cmd, Dir: dir, Started: time.Now(), OutPath: outPath, ExitCode: -1}
	shell, sargs := ShellCommand(cmd)
	// timeout <= 0 — без ограничения: для фоновой задачи это и есть смысл,
	// иначе она отличалась бы от bash только задержкой вывода.
	ctx := context.Background()
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	}
	ce := exec.CommandContext(ctx, shell, sargs...)
	ce.Dir = dir
	ce.Env = append(os.Environ(), "GCLI=1")
	ce.Stdout, ce.Stderr = f, f

	j.mu.Lock()
	j.stop = func() { _ = ce.Process.Kill() }
	j.mu.Unlock()

	if err := ce.Start(); err != nil {
		cancel()
		// Пишем в лог ДО закрытия файла: после Close() запись молча
		// игнорируется, и единственное объяснение, почему задача не
		// запустилась, терялось.
		_, _ = f.WriteString(fmt.Sprintf("[не запустилась: %v]\n", err))
		_ = f.Close()
		j.mu.Lock()
		j.Err = err.Error()
		j.ExitCode = -1
		j.FinishedAt = time.Now()
		j.mu.Unlock()
		registerJob(j)
		return j
	}

	var once sync.Once
	go func() {
		werr := ce.Wait()
		_ = f.Close()
		cancel()
		once.Do(func() {
			j.mu.Lock()
			// Код выхода читаем именно здесь: после Wait() ProcessState
			// заполнен, а раньше он nil, и обращение к нему упало бы с паникой.
			code := -1
			if ce.ProcessState != nil {
				code = ce.ProcessState.ExitCode()
			}
			j.ExitCode = code
			j.FinishedAt = time.Now()
			if werr != nil {
				j.Err = werr.Error()
			}
			j.mu.Unlock()
		})
	}()

	registerJob(j)
	return j
}

// registerJob — положить задачу в реестр, вытеснив самые старые.
func registerJob(j *Job) {
	jobRegistry.mu.Lock()
	defer jobRegistry.mu.Unlock()
	jobRegistry.jobs[j.ID] = j
	if len(jobRegistry.jobs) <= maxJobs {
		return
	}
	// Вытесняем самые старые завершённые, а если все заняты — самые старые вообще.
	type aged struct {
		id  string
		age time.Time
	}
	list := make([]aged, 0, len(jobRegistry.jobs))
	for id, jj := range jobRegistry.jobs {
		if id == j.ID {
			continue
		}
		t := jj.Started
		if !jj.FinishedAt.IsZero() {
			t = jj.FinishedAt
		}
		list = append(list, aged{id: id, age: t})
	}
	for len(jobRegistry.jobs) > maxJobs && len(list) > 0 {
		sort.Slice(list, func(i, k int) bool { return list[i].age.Before(list[k].age) })
		delete(jobRegistry.jobs, list[0].id)
		list = list[1:]
	}
}

// jobByID — найти задачу по ID.
func jobByID(id string) *Job {
	jobRegistry.mu.Lock()
	defer jobRegistry.mu.Unlock()
	return jobRegistry.jobs[id]
}

// jobList — задачи по убыванию времени запуска.
func jobList() []*Job {
	jobRegistry.mu.Lock()
	defer jobRegistry.mu.Unlock()
	out := make([]*Job, 0, len(jobRegistry.jobs))
	for _, j := range jobRegistry.jobs {
		out = append(out, j)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Started.After(out[k].Started) })
	return out
}

// jobListText — таблица фоновых задач.
func jobListText() (string, string) {
	list := jobList()
	if len(list) == 0 {
		return "Фоновых процессов нет.", "фоновых процессов нет"
	}
	var b strings.Builder
	running := 0
	for _, j := range list {
		if j.Status() == jobRunning {
			running++
		}
		elapsed := core.HumanDuration(time.Since(j.Started))
		if !j.FinishedAt.IsZero() {
			elapsed = core.HumanDuration(j.FinishedAt.Sub(j.Started))
		}
		fmt.Fprintf(&b, "- %s  [%s]  %s  (вела %s)\n", j.ID, j.Status(),
			core.Truncate(core.OneLine(j.Command), 70), elapsed)
	}
	fmt.Fprintf(&b, "\nВсего: %d, выполняется: %d.\nЗабрать вывод: job {action:\"output\", id:\"job1\"}", len(list), running)
	return strings.TrimSpace(b.String()), fmt.Sprintf("процессов: %d, работает: %d", len(list), running)
}

// hJob — управление фоновыми процессами.
func (r *Registry) hJob(_ context.Context, m map[string]any) (Result, error) {
	action := strings.ToLower(strings.TrimSpace(ArgStr(m, "action")))
	if action == "" {
		action = "status"
	}
	if action == "run" || action == "start" {
		cmd := strings.TrimSpace(ArgStr(m, "command"))
		if cmd == "" {
			return Result{}, fmt.Errorf("укажи command")
		}
		dir := r.workDir
		if wd := ArgStr(m, "workdir"); wd != "" {
			var err error
			if dir, err = r.pathArg(wd); err != nil {
				return Result{}, err
			}
		}
		if err := r.guardCommand(cmd, dir); err != nil {
			return Result{}, err
		}
		if r.env.Confirm != nil {
			if !r.env.Confirm(ConfirmReq{Kind: ConfirmExec, Detail: cmd, Reason: "фоновый процесс"}) {
				return Result{Text: "Запуск отменён", Summary: "отменено"}, nil
			}
		}
		j := startJob(cmd, dir, ArgInt(m, "timeout_sec", 0))
		if j.Err != "" && j.OutPath == "" {
			return Result{}, fmt.Errorf("не удалось запустить в фоне: %s", j.Err)
		}
		return Result{
			Text: fmt.Sprintf("Запущено в фоне: %s\nID: %s\nЛог: %s\n\n"+
				"Дождись и забери результат: job {action:\"output\", id:\"%s\"} (покажет и код выхода). "+
				"Список: job {action:\"status\"}.",
				j.Command, j.ID, j.OutPath, j.ID),
			Summary: "фон: " + j.ID,
		}, nil
	}

	switch action {
	case "status", "list", "":
		txt, sum := jobListText()
		return Result{Text: txt, Summary: sum}, nil

	case "output", "out", "log":
		id := ArgStr(m, "id")
		if id == "" {
			// Без ID берём последнюю: почти всегда интересует именно она.
			if list := jobList(); len(list) > 0 {
				id = list[0].ID
			}
		}
		j := jobByID(id)
		if j == nil {
			return Result{}, fmt.Errorf("нет такого процесса: %s (посмотри job {action:\"status\"})", id)
		}
		if j.OutPath == "" {
			return Result{}, fmt.Errorf("у %s нет вывода: %s", j.ID, j.Err)
		}
		data, err := os.ReadFile(j.OutPath)
		if err != nil {
			return Result{}, err
		}
		txt := core.TruncateUTF8(string(data), 6000, 3000)
		st := j.Status()
		tail := ""
		if code, ok := j.Exit(); ok {
			verdict := "успех"
			switch st {
			case jobFailed:
				verdict = fmt.Sprintf("ОШИБКА, код выхода %d", code)
			case jobUnknown:
				verdict = fmt.Sprintf("код выхода неизвестен (%d) — процесс убит или завершился не штатно", code)
			}
			tail = fmt.Sprintf("\n\n[статус: %s; код выхода: %d — %s]", st, code, verdict)
		} else {
			tail = "\n\n[процесс ещё выполняется — загляни позже]"
		}
		return Result{
			Text:    txt + tail,
			Summary: fmt.Sprintf("вывод %s: %s", j.ID, st),
		}, nil

	case "stop", "kill":
		id := ArgStr(m, "id")
		j := jobByID(id)
		if j == nil {
			return Result{}, fmt.Errorf("нет такого процесса: %s", id)
		}
		// Проверяем завершённость под той же блокировкой, которой потом
		// меняем Stopped: иначе гонка между «проверили, что жив» и
		// «проставили Stopped» красила бы уже завершившуюся задачу как
		// остановленную нами.
		j.mu.Lock()
		alive := j.statusLocked() == jobRunning
		fn := j.stop
		if alive {
			j.Stopped = true
		}
		j.mu.Unlock()
		if !alive {
			return Result{
				Text:    fmt.Sprintf("Процесс %s уже %s — останавливать нечего.", j.ID, j.Status()),
				Summary: "уже завершён",
			}, nil
		}
		if fn != nil {
			fn()
		}
		return Result{Text: "Процесс " + j.ID + " остановлен.", Summary: "остановлен " + j.ID}, nil

	case "clear", "clean":
		jobRegistry.mu.Lock()
		n := 0
		for id, j := range jobRegistry.jobs {
			if j.Done() {
				delete(jobRegistry.jobs, id)
				n++
			}
		}
		jobRegistry.mu.Unlock()
		return Result{
			Text:    fmt.Sprintf("Убрано завершённых процессов из списка: %d.", n),
			Summary: fmt.Sprintf("убрано: %d", n),
		}, nil
	}
	return Result{}, fmt.Errorf("неизвестное действие: %s (run, status, output, stop, clear)", action)
}
