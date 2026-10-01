package core

// Журнал автономного прогона: append-only журнал, переживающий падение.
//
// Зачем он, если есть mission_state.json: тот файл перезаписывается целиком
// и хранит только последний снимок. За многочасовой прогон снимков десятки,
// и при падении на записи (свет выключили, процесс убили) на диске остаётся
// либо ничего, либо половина JSON. Журнал решает обе проблемы сразу:
//
//   - запись идёт ТОЛЬКО добавлением в конец, без перезаписи: оборванная
//     запись физически не может испортить предыдущие;
//   - каждая запись несёт свою длину и контрольную сумму, поэтому читатель
//     знает, где запись кончилась, даже если хвост файла оборван.
//
// Правило восстановления простое и потому надёжное: читаем до первой битой
// записи и отбрасываем хвост. Лучше потерять последний чекпоинт, чем
// подсунуть модели снимок, написанный наполовину.

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"time"
)

// maxJournalRecord — потолок одной записи журнала.
//
// 1 МиБ с запасом: запись — короткий снимок состояния, а не лог запросов.
// Если она оказалась больше, это уже не снимок, и такую запись лучше
// отбросить, чем втихую портить файл.
const maxJournalRecord = 1 << 20

// journalSumLen — ширина контрольной суммы в символах (hex crc32).
const journalSumLen = 8

// journalMagic — метка записи журнала.
//
// Однобайтовая, а не длинная строка: она должна отличать запись от мусора в
// начале файла, а не занимать место. Всё остальное проверяет контрольная
// сумма, и случайно подделать её невозможно.
const journalMagic = "\x00gJ"

// События журнала.
const (
	// JournalStart — прогон начался.
	JournalStart = "start"
	// JournalCheckpoint — очередной чекпоинт состояния.
	JournalCheckpoint = "checkpoint"
	// JournalStop — прогон остановлен необратимо, с причиной.
	//
	// Это про конец работы: бюджет исчерпан, критерии закрыты, человек
	// сказал «стоп». Продолжать такой прогон бессмысленно, и resume после
	// него не предлагается.
	JournalStop = "stop"
	// JournalClose — сеанс закрылся, а прогон не окончен.
	//
	// Отдельный вид от JournalStop именно потому, что это разные вещи:
	// Ctrl+D вечером не значит «работа сделана», но означает «журнал до
	// сюда цел». Смешать их — значит либо предлагать продолжение после
	// исчерпанного бюджета, либо потерять честную точку возврата после
	// обычного выхода.
	JournalClose = "close"
	// JournalResume — прогон продолжен после перезапуска.
	JournalResume = "resume"
)

// JournalRecord — одна запись журнала.
type JournalRecord struct {
	// TS — время записи в RFC3339. Пишется текстом, а не числом: журнал
	// должен оставаться читаемым в текстовом редакторе, когда человек
	// пришёл разбираться, почему прогон встал.
	TS string `json:"ts"`
	// Kind — что за запись: start, checkpoint, stop, crash, resume.
	Kind string `json:"kind,omitempty"`
	// Summary — короткое описание состояния.
	Summary string `json:"summary,omitempty"`
	// Iters, ToolCalls, Tokens — счётчики на момент записи.
	Iters     int `json:"iters,omitempty"`
	ToolCalls int `json:"tool_calls,omitempty"`
	Tokens    int `json:"tokens,omitempty"`
	// SpentUSD — расход в долларах на момент записи.
	SpentUSD float64 `json:"spent_usd,omitempty"`
	// Reason — причина остановки (для записей вида stop).
	Reason string `json:"reason,omitempty"`
	// Objective и Mode — чтобы восстановить рамку прогона из журнала
	// одного, даже если mission.json к этому моменту переписан руками.
	Objective string `json:"objective,omitempty"`
	Mode      string `json:"mode,omitempty"`
	// Tools — имена инструментов последнего шага. Короткий список помогает
	// модели сориентироваться, на чём именно работа встала.
	Tools []string `json:"tools,omitempty"`
}

// missionRecord — запись журнала в виде снимка MissionState.
//
// Сделано один раз и явно, потому что чекпоинт и resume обязаны читать
// одни и те же поля: расхождение в одной строке здесь означало бы, что
// после перезапуска модель увидит половину того, что видела до обрыва.
func missionRecord(kind, summary string, tr *Tracker) JournalRecord {
	rec := JournalRecord{Kind: kind, Summary: summary}
	if tr == nil {
		return rec
	}
	m := tr.Mission()
	rec.Objective = m.Objective
	rec.Mode = string(m.Mode)
	rec.Iters = tr.Iters()
	rec.ToolCalls = tr.ToolCalls()
	rec.Tokens = tr.Spent()
	rec.SpentUSD = tr.Cost("", "")
	rec.Reason = tr.Stopped()
	return rec
}

// ---------- Чтение ----------

// ReadJournal — прочитать журнал, отбросив битый хвост.
//
// Возвращает записи до первой сломанной. Битых записей может оказаться
// много, но читать дальше бессмысленно: после обрыва порядок уже не
// восстановить, и записи после него относятся к непредсказуемому моменту.
// tailOK=false означает «файл обрезан», и это повод сказать об этом
// человеку, а не молча продолжить.
func ReadJournal(path string) (recs []JournalRecord, tailOK bool, err error) {
	if path == "" {
		return nil, true, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, true, nil
		}
		return nil, false, err
	}
	off := 0
	for off < len(data) {
		rec, next, ok := decodeRecord(data, off)
		if !ok {
			return recs, false, nil
		}
		recs = append(recs, rec)
		off = next
	}
	return recs, true, nil
}

// decodeRecord — разобрать одну запись по смещению.
//
// ok=false означает «здесь записи больше нет» — это и штатный конец файла, и
// обрыв. Различить их может только вызывающий, поэтому неудачный разбор
// молча возвращает false, а не частично разобранную запись.
func decodeRecord(data []byte, off int) (JournalRecord, int, bool) {
	if off < 0 || off >= len(data) {
		return JournalRecord{}, off, false
	}
	nl := bytes.IndexByte(data[off:], '\n')
	if nl < 0 {
		// Хвост без перевода строки — запись оборвалась на середине.
		return JournalRecord{}, off, false
	}
	line := data[off : off+nl]
	if len(line) == 0 {
		// Пустая строка внутри журнала: не запись, просто пропуск.
		return JournalRecord{}, off + nl + 1, true
	}
	if !bytes.HasPrefix(line, []byte(journalMagic)) {
		return JournalRecord{}, off, false
	}
	hdr := len(journalMagic) + 4
	if len(line) < hdr+journalSumLen {
		return JournalRecord{}, off, false
	}
	length := int(binary.BigEndian.Uint32(line[len(journalMagic):hdr]))
	if length < 0 || length > maxJournalRecord || len(line) < hdr+length+journalSumLen {
		return JournalRecord{}, off, false
	}
	raw := line[hdr : hdr+length]
	sum := line[hdr+length:]
	// Регистр не важен: сумма могла быть записана в hex любой буквы,
	// а сравнение по байтам не должно зависеть от этого.
	if !bytes.EqualFold(sum, journalSum(raw)) {
		return JournalRecord{}, off, false
	}
	var rec JournalRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return JournalRecord{}, off, false
	}
	return rec, off + nl + 1, true
}

// journalSum — контрольная сумма записи в виде hex-строки.
func journalSum(raw []byte) []byte {
	var b [4]byte
	v := crc32.ChecksumIEEE(raw)
	binary.BigEndian.PutUint32(b[:], v)
	out := make([]byte, journalSumLen)
	hex.Encode(out, b[:])
	return out
}

// LastJournalRecord — последняя осмысленная запись журнала.
//
// Осмысленная — с распознанным временем: запись без метки времени не может
// ответить на главный вопрос восстановления «когда это было».
func LastJournalRecord(path string) (rec JournalRecord, has bool, truncated bool) {
	recs, ok, err := ReadJournal(path)
	if err != nil {
		return JournalRecord{}, false, false
	}
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].TS == "" {
			continue
		}
		return recs[i], true, !ok
	}
	return JournalRecord{}, false, !ok
}

// ---------- Запись ----------

// Journal — писатель в журнал прогона.
//
// Держит открытый файл, потому что переоткрывать его на каждой записи
// во время многочасового прогона дорого, а на Windows ещё и означает
// rename-замену, которую антивирус держит на проверке.
type Journal struct {
	// path — куда пишем.
	path string
	// f — открытый файл, nil до первой записи (ленивое открытие).
	f *os.File
	// err — первая ошибка записи. Дальше повторять бессмысленно: если
	// диск кончился, то и на следующей записи будет так же, и вызывающий
	// узнает об этом один раз вместо тысячи.
	err error
	// wrote — была ли запись о ходе работы. Открытие журнала и запись
	// «resume» само по себе её не считают: иначе сеанс, который
	// подхватил прогон и сразу вышел, дописал бы «сеанс закрыт» поверх
	// «продолжено», и следующий запуск предложил бы то же продолжение
	// снова.
	wrote bool
}

// Wrote — были ли в этом запуске записи о ходе работы.
//
// Отвечает на вопрос «стоит ли дописывать что-то при выходе»: после
// пустого сеанса запись «закрыто» была бы шумом, а её отсутствие не
// мешает чтению — журнал просто заканчивается последним чекпоинтом.
func (j *Journal) Wrote() bool { return j != nil && j.wrote }

// JournalPath — файл журнала прогона.
func JournalPath(workDir string) string {
	return filepath.Join(workDir, ".gcli", "mission_journal.log")
}

// OpenJournal — открыть журнал на дописывание.
//
// Существующий журнал НЕ обрезается: он переживает перезапуск, и это его
// главное свойство. Обрезать его при старте значило бы выбросить ровно ту
// историю, ради которой он существует.
func OpenJournal(workDir string) (*Journal, error) {
	path := JournalPath(workDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return &Journal{path: path}, nil
}

// Close — закрыть журнал, сбросив на диск.
func (j *Journal) Close() error {
	if j == nil || j.f == nil {
		return nil
	}
	f := j.f
	j.f = nil
	// Sync до Close: Close сливает только буферы пользователя, а при
	// выключенном свете посреди прогона запись может не дойти до пластины.
	// Прогон рассчитан на переживание обрыва, поэтому fsync здесь не
	// оптимизация, а часть контракта.
	_ = f.Sync()
	return f.Close()
}

// Append — дописать запись и сбросить на диск.
//
// fsync после каждой записи — намеренная плата: без него запись может
// потеряться при выключении, а прогон на 12 часов с бюджетом в десятки
// долларов не имеет права начинаться заново из-за обеда питания.
func (j *Journal) Append(rec JournalRecord) error {
	if j == nil {
		return nil
	}
	// «Resume» и «start» — это вход в прогон, а не ход работы внутри него.
	// Считать их прогрессом нельзя: сеанс, который только подхватил
	// прогон и вышел, не сделал ничего.
	if rec.Kind != JournalStart && rec.Kind != JournalResume {
		j.wrote = true
	}
	if j.err != nil {
		return j.err
	}
	if rec.TS == "" {
		rec.TS = time.Now().Format(time.RFC3339)
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if len(raw) > maxJournalRecord {
		j.err = fmt.Errorf("запись прогона %d байт велика, журнал её не примет", len(raw))
		return j.err
	}
	if j.f == nil {
		f, err := os.OpenFile(j.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			j.err = err
			return err
		}
		j.f = f
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(raw)))
	line := make([]byte, 0, len(journalMagic)+4+len(raw)+journalSumLen+1)
	line = append(line, journalMagic...)
	line = append(line, hdr[:]...)
	line = append(line, raw...)
	line = append(line, journalSum(raw)...)
	line = append(line, '\n')
	if _, err := j.f.Write(line); err != nil {
		j.err = err
		return err
	}
	// Ошибка Sync не значит, что запись не ушла: Write уже вернул nil, то
	// есть данные в кэше ОС. Помечать журнал сломанным на этом основании
	// нельзя — иначе одно «устройство занято» обрывало бы прогон.
	if err := j.f.Sync(); err != nil {
		return err
	}
	return nil
}

// Checkpoint — записать снимок состояния прогона.
func (j *Journal) Checkpoint(summary string, tr *Tracker) error {
	return j.Append(missionRecord(JournalCheckpoint, summary, tr))
}

// Start — записать начало прогона.
func (j *Journal) Start(summary string, tr *Tracker) error {
	return j.Append(missionRecord(JournalStart, summary, tr))
}

// Stop — записать необратимую остановку с причиной.
func (j *Journal) Stop(summary string, tr *Tracker) error {
	return j.Append(missionRecord(JournalStop, summary, tr))
}

// CloseRecord — записать, что сеанс закрылся, а прогон не окончен.
//
// Имя с уточнением, потому что Close у Journal уже занят под закрытие
// файла: два «закрыть» в одном типе — это ровно та двусмысленность,
// из-за которой обычный уход сначала выглядел остановкой прогона.
func (j *Journal) CloseRecord(summary string, tr *Tracker) error {
	return j.Append(missionRecord(JournalClose, summary, tr))
}

// Resumed — записать, что прогон подхватили после обрыва.
func (j *Journal) Resumed(summary string, tr *Tracker) error {
	return j.Append(missionRecord(JournalResume, summary, tr))
}

// ---------- Восстановление ----------

// MissionResumeFromJournal — восстановить снимок прогона из журнала.
//
// has=false означает «восстанавливать нечего», и это не ошибка: журнала
// может просто не быть. truncated=true — журнал обрезан, то есть последняя
// запись потеряна; это не ошибка, а факт, о котором стоит сказать.
//
// Два случая, когда продолжение не предлагается, и оба важны:
//
//   - последняя запись — resume: прогон уже продолжали в этом месте,
//     второе «продолжить с прошлого» только запутало бы;
//   - у последней записи есть причина остановки: прогон закрыт
//     необратимо (бюджет, критерии, «стоп»), и подхват снова вс��
//     его на первой же итерации — человек увидел бы «продолжаем», а
//     через секунду «остановлен: срок вышел».
func MissionResumeFromJournal(path string, now time.Time, within time.Duration) (st MissionState, has bool, truncated bool) {
	rec, ok, cut := LastJournalRecord(path)
	if !ok {
		return MissionState{}, false, cut
	}
	if rec.Kind == JournalResume {
		return MissionState{}, false, cut
	}
	if rec.Reason != "" {
		// Причина приходит из трекера, поэтому она есть у любой записи,
		// сделанной после остановки, — включая финальный чекпоинт.
		return MissionState{}, false, cut
	}
	if !FreshMissionState(MissionState{Updated: rec.TS}, now, within) {
		// Свежесть проверяется по метке времени записи: снимок недельной
		// давности предложение «продолжить» превратилось бы в предложение
		// начать заново, но с чужими счётчиками внутри.
		return MissionState{}, false, cut
	}
	return MissionState{
		Objective:  rec.Objective,
		Mode:       rec.Mode,
		Summary:    rec.Summary,
		Status:     rec.Summary,
		StopReason: rec.Reason,
		Iters:      rec.Iters,
		ToolCalls:  rec.ToolCalls,
		Tokens:     rec.Tokens,
		Updated:    rec.TS,
	}, true, cut
}
