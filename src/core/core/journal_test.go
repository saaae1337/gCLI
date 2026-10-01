package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------- Запись и чтение ----------

func TestJournalRoundTrip(t *testing.T) {
	work := t.TempDir()
	j, err := OpenJournal(work)
	if err != nil {
		t.Fatal(err)
	}
	tr := NewTracker(Mission{Mode: MissionLongTime, Objective: "починить тесты"}.Apply(), 0, 0)
	tr.Tick(7, 12, 5000)
	if err := j.Checkpoint("проверил сборку", tr); err != nil {
		t.Fatal(err)
	}
	// Причина берётся из трекера, а не из аргумента: трекер — источник
	// истины, и подставить в журнал то, чего не было, нельзя.
	tr.Stop(StopDeadline)
	if err := j.Stop("срок вышел", tr); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	recs, ok, err := ReadJournal(JournalPath(work))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("журнал, записанный нами, не может считаться обрезанным")
	}
	if len(recs) != 2 {
		t.Fatalf("записей %d, хотели 2", len(recs))
	}
	last := recs[1]
	if last.Kind != JournalStop {
		t.Errorf("вид записи %q", last.Kind)
	}
	if last.Iters != 7 || last.ToolCalls != 12 || last.Tokens != 5000 {
		t.Errorf("счётчики исказились: %+v", last)
	}
	if last.Objective != "починить тесты" || last.Mode != string(MissionLongTime) {
		t.Errorf("рамка прогона потерялась: %+v", last)
	}
	if last.Reason != StopDeadline {
		t.Errorf("причина остановки %q, хотели %q", last.Reason, StopDeadline)
	}
	if last.Summary != "срок вышел" {
		t.Errorf("описание %q", last.Summary)
	}
}

func TestJournalSurvivesReopen(t *testing.T) {
	// Журнал переживает перезапуск: второй запуск дописывает, а не
	// начинает файл заново. Это и есть смысл append-only.
	work := t.TempDir()
	j, err := OpenJournal(work)
	if err != nil {
		t.Fatal(err)
	}
	tr := NewTracker(Mission{Mode: MissionLongTime}.Apply(), 0, 0)
	if err := j.Start("начало", tr); err != nil {
		t.Fatal(err)
	}
	_ = j.Close()

	j2, err := OpenJournal(work)
	if err != nil {
		t.Fatal(err)
	}
	if err := j2.Checkpoint("второй запуск", tr); err != nil {
		t.Fatal(err)
	}
	_ = j2.Close()

	recs, ok, err := ReadJournal(JournalPath(work))
	if err != nil {
		t.Fatal(err)
	}
	if !ok || len(recs) != 2 {
		t.Fatalf("журнал не накопился: ok=%v записей %d", ok, len(recs))
	}
}

// ---------- Обрыв и порча ----------

// appendGarbage — дописать в журнал мусор, как это делает упавший процесс.
func appendGarbage(t *testing.T, path, garbage string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(garbage); err != nil {
		t.Fatal(err)
	}
}

func TestJournalTruncatedTail(t *testing.T) {
	// Свет выключили посреди записи: хвост без перевода строки.
	// Читать надо всё, что до обрыва, и честно сказать, что хвост потерян.
	work := t.TempDir()
	j, _ := OpenJournal(work)
	tr := NewTracker(Mission{Mode: MissionLongTime}.Apply(), 0, 0)
	_ = j.Checkpoint("первый", tr)
	_ = j.Checkpoint("второй", tr)
	_ = j.Close()
	appendGarbage(t, JournalPath(work), "\x00gJ\x00\x00\x10{\"ts\":\"2026")

	recs, ok, err := ReadJournal(JournalPath(work))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("оборванный хвост обязан быть замечен")
	}
	if len(recs) != 2 {
		t.Fatalf("до обрыва %d записей, хотели 2", len(recs))
	}
}

func TestJournalBadChecksumStopsReading(t *testing.T) {
	// Испорченная сумма — это не «запись с правками», а обрыв: дальше
	// читать нельзя, потому что не известно, что там было.
	work := t.TempDir()
	j, _ := OpenJournal(work)
	tr := NewTracker(Mission{Mode: MissionLongTime}.Apply(), 0, 0)
	_ = j.Checkpoint("первый", tr)
	_ = j.Checkpoint("второй", tr)
	_ = j.Close()

	path := JournalPath(work)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Портим один байт в середине файла: первая запись ещё цела, вторая — нет.
	i := strings.Index(string(data), "второй")
	if i < 0 {
		t.Fatalf("в журнале нет второй записи:\n%s", data)
	}
	bad := append([]byte(nil), data...)
	bad[i] ^= 0xff
	if err := os.WriteFile(path, bad, 0o644); err != nil {
		t.Fatal(err)
	}

	recs, ok, err := ReadJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("порча суммы обязана читаться как обрыв")
	}
	if len(recs) != 1 {
		t.Errorf("сохраниться должны были только записи до порчи, получили %d", len(recs))
	}
}

func TestJournalGarbageAtStart(t *testing.T) {
	// Чужой файл в месте журнала: читать нечего, ломаться нельзя.
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(JournalPath(work)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(JournalPath(work), []byte("это не журнал\nвовсе\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recs, ok, err := ReadJournal(JournalPath(work))
	if err != nil {
		t.Fatalf("чужой файл не должен считаться ошибкой чтения: %v", err)
	}
	if ok || len(recs) != 0 {
		t.Errorf("мусор не должен читаться как записи: ok=%v записей %d", ok, len(recs))
	}
}

func TestJournalEmptyAndAbsent(t *testing.T) {
	work := t.TempDir()
	recs, ok, err := ReadJournal(JournalPath(work))
	if err != nil || !ok || len(recs) != 0 {
		t.Errorf("нет журнала — это не ошибка: ok=%v err=%v записей %d", ok, err, len(recs))
	}
	// Пустой файл читается как «ничего не записано», а не как обрыв.
	empty := filepath.Join(work, "empty.log")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	recs, ok, err = ReadJournal(empty)
	if err != nil || !ok || len(recs) != 0 {
		t.Errorf("пустой журнал: ok=%v err=%v записей %d", ok, err, len(recs))
	}
}

// ---------- Восстановление ----------

func TestMissionResumeFromJournal(t *testing.T) {
	work := t.TempDir()
	j, _ := OpenJournal(work)
	tr := NewTracker(Mission{Mode: MissionLongTime, Objective: "починить тесты"}.Apply(), 0, 0)
	tr.Tick(9, 20, 30000)
	_ = j.Checkpoint("тесты зелёные", tr)
	_ = j.Close()

	st, has, cut := MissionResumeFromJournal(JournalPath(work), time.Now(), 24*time.Hour)
	if !has {
		t.Fatal("свежий журнал обязан давать восстановление")
	}
	if cut {
		t.Error("полный журнал не обрезан")
	}
	if st.Objective != "починить тесты" {
		t.Errorf("цель %q", st.Objective)
	}
	if st.Iters != 9 || st.ToolCalls != 20 || st.Tokens != 30000 {
		t.Errorf("счётчики исказились: %+v", st)
	}
	if !strings.Contains(st.Summary, "тесты зелёные") {
		t.Errorf("описание %q", st.Summary)
	}
}

func TestMissionResumeSkipsOldJournal(t *testing.T) {
	// Журнал недельной давности — это уже другая работа. Предлагать
	// «продолжить» значило бы начать заново с чужими счётчиками внутри.
	work := t.TempDir()
	j, _ := OpenJournal(work)
	tr := NewTracker(Mission{Mode: MissionLongTime}.Apply(), 0, 0)
	rec := missionRecord(JournalCheckpoint, "давно", tr)
	rec.TS = time.Now().Add(-72 * time.Hour).Format(time.RFC3339)
	_ = j.Append(rec)
	_ = j.Close()

	if _, has, _ := MissionResumeFromJournal(JournalPath(work), time.Now(), 24*time.Hour); has {
		t.Error("журнал трёхдневной давности не должен предлагаться к продолжению")
	}
}

func TestMissionResumeSkipsAfterResumeRecord(t *testing.T) {
	// Повторный resume не предлагается: прогон уже продолжали.
	work := t.TempDir()
	j, _ := OpenJournal(work)
	tr := NewTracker(Mission{Mode: MissionLongTime}.Apply(), 0, 0)
	_ = j.Checkpoint("до падения", tr)
	_ = j.Append(JournalRecord{Kind: JournalResume, TS: time.Now().Format(time.RFC3339)})
	_ = j.Close()

	if _, has, _ := MissionResumeFromJournal(JournalPath(work), time.Now(), 24*time.Hour); has {
		t.Error("после записи о продолжении предлагать ещё раз нельзя")
	}
}

func TestMissionResumeReportsTruncation(t *testing.T) {
	// Обрезанный журнал всё равно годится для продолжения — просто
	// человек должен знать, что последний чекпоинт потерян.
	work := t.TempDir()
	j, _ := OpenJournal(work)
	tr := NewTracker(Mission{Mode: MissionLongTime}.Apply(), 0, 0)
	_ = j.Checkpoint("целое", tr)
	_ = j.Close()
	appendGarbage(t, JournalPath(work), "\x00gJ\x00")

	_, has, cut := MissionResumeFromJournal(JournalPath(work), time.Now(), 24*time.Hour)
	if !has {
		t.Fatal("целые записи до обрыва должны давать восстановление")
	}
	if !cut {
		t.Error("обрыв обязан быть виден вызывающему")
	}
}

func TestJournalNilSafe(t *testing.T) {
	var j *Journal
	if err := j.Append(JournalRecord{}); err != nil {
		t.Errorf("nil-журнал не должен возвращать ошибку: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Errorf("nil-журнал не должен паниковать на закрытии: %v", err)
	}
	if err := j.Checkpoint("x", nil); err != nil {
		t.Errorf("nil-журнал: %v", err)
	}
	// Отсутствие трекера — не ошибка: запись без счётчиков всё равно полезна.
	work := t.TempDir()
	real, err := OpenJournal(work)
	if err != nil {
		t.Fatal(err)
	}
	if err := real.Checkpoint("без трекера", nil); err != nil {
		t.Fatal(err)
	}
	_ = real.Close()
	recs, _, _ := ReadJournal(JournalPath(work))
	if len(recs) != 1 || recs[0].Summary != "без трекера" {
		t.Errorf("запись без трекера не записалась: %+v", recs)
	}
}

func TestJournalLastRecordPicksNewest(t *testing.T) {
	work := t.TempDir()
	j, _ := OpenJournal(work)
	tr := NewTracker(Mission{Mode: MissionLongTime}.Apply(), 0, 0)
	tr.Tick(1, 2, 30)
	_ = j.Checkpoint("раньше", tr)
	tr.Tick(5, 9, 90)
	_ = j.Checkpoint("позже", tr)
	_ = j.Close()

	rec, has, _ := LastJournalRecord(JournalPath(work))
	if !has {
		t.Fatal("запись обязана найтись")
	}
	if !strings.Contains(rec.Summary, "позже") || rec.Iters != 5 {
		t.Errorf("вернулась не последняя запись: %+v", rec)
	}
}
