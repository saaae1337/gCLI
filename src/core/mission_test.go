package core

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------- Режимы и пресеты ----------

func TestMissionModeParse(t *testing.T) {
	cases := map[string]MissionMode{
		"":                MissionNormal,
		"normal":          MissionNormal,
		"  long-time ":    MissionLongTime,
		"LONG-TIME":       MissionLongTime,
		"extra-long-time": MissionExtraLong,
		"overnight":       MissionOvernight,
	}
	for in, want := range cases {
		got, ok := ValidMissionMode(in)
		if !ok || got != want {
			t.Errorf("ValidMissionMode(%q) = %q, %v; хотели %q", in, got, ok, want)
		}
	}
	if _, ok := ValidMissionMode("вечность"); ok {
		t.Error("неизвестный режим не должен проходить как допустимый")
	}
}

func TestMissionNormalizeRejectsUnknownMode(t *testing.T) {
	m := Mission{Mode: "eternal"}
	err := m.Normalize()
	if err == nil {
		t.Fatal("ожидалась ошибка на неизвестном режиме")
	}
	// В сообщении должны быть перечислены режимы: иначе человек
	// гадает, что он написал не так.
	for _, want := range []string{"normal", "long-time", "extra-long-time"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в ошибке нет подсказки %q: %v", want, err)
		}
	}
}

// TestMissionApplyDoesNotRaiseDeadline — ключевое правило пресетов.
//
// Если человек задал срок сам, смена режима не должна его тихо менять:
// режим задаёт умолчания, а не приказы.
func TestMissionApplyDoesNotRaiseDeadline(t *testing.T) {
	m := Mission{Mode: MissionExtraLong, Deadline: Dur(time.Hour)}.Apply()
	if m.Deadline != Dur(time.Hour) {
		t.Errorf("явно заданный срок перебит пресетом: %s", m.Deadline)
	}
}

func TestMissionApplyFillsFromPreset(t *testing.T) {
	m := Mission{Mode: MissionLongTime}.Apply()
	if m.Deadline != Dur(4*time.Hour) {
		t.Errorf("long-time должен давать 4h, получили %s", m.Deadline)
	}
	if m.MaxIters != 200 {
		t.Errorf("long-time должен давать 200 итераций, получили %d", m.MaxIters)
	}
	if m.CheckpointEvery != 10 {
		t.Errorf("long-time должен сохранять состояние каждые 10 итераций, получили %d", m.CheckpointEvery)
	}
	// Нормальный режим не должен вводить потолков: он и так живёт
	// по лимитам агента.
	n := Mission{Mode: MissionNormal}.Apply()
	if n.MaxIters != 0 || n.Deadline != 0 {
		t.Errorf("normal не должен задавать потолки, получил iters=%d deadline=%s", n.MaxIters, n.Deadline)
	}
}

func TestMissionOvernightCheckpointsFrequent(t *testing.T) {
	// Ночной прогон рассчитан на перезапуск компьютера: состояние
	// должно сохраняться чаще, чем в длинном дневном.
	overnight := Mission{Mode: MissionOvernight}.Apply()
	long := Mission{Mode: MissionLongTime}.Apply()
	if overnight.CheckpointEvery == 0 {
		t.Fatal("overnight обязан сохранять состояние")
	}
	if overnight.CheckpointEvery >= long.CheckpointEvery {
		t.Errorf("overnight (%d) должен сохранять чаще long-time (%d)",
			overnight.CheckpointEvery, long.CheckpointEvery)
	}
}

// ---------- Длительность ----------

func TestParseDur(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"4h", 4 * time.Hour},
		{"90m", 90 * time.Minute},
		{"1h30m", 90 * time.Minute},
		{"", 0},
		// Голое число — минуты: «30» про работу агента это полчаса.
		{"30", 30 * time.Minute},
		{"45", 45 * time.Minute},
	}
	for _, c := range cases {
		got, err := ParseDur(c.in)
		if err != nil {
			t.Errorf("ParseDur(%q): %v", c.in, err)
			continue
		}
		if got.D() != c.want {
			t.Errorf("ParseDur(%q) = %s, хотели %s", c.in, got.D(), c.want)
		}
	}
	if _, err := ParseDur("-5h"); err == nil {
		t.Error("отрицательная длительность должна отвергаться")
	}
	if _, err := ParseDur("завтра"); err == nil {
		t.Error("мусор должен отвергаться")
	}
}

// TestParseDurRejectsOverflow — регрессия: голое число минут умножалось на
// time.Minute без проверки, и «1077000000» (чуть больше 2^63 наносекунд)
// молча превращалось в отрицательный срок. Миссия с таким дедлайном
// запускалась «просроченной» и обрывалась, вместо того чтобы сказать, что
// срок неправдоподобен. Этот вход нашёл go-fuzz, тест держит его от regress.
func TestParseDurRejectsOverflow(t *testing.T) {
	maxMinutes := int64(math.MaxInt64) / int64(time.Minute)
	for _, s := range []string{
		"1077000000",                     // граница переполнения, найдена фаззингом
		fmt.Sprintf("%d", maxMinutes+1),  // ровно за пределом
		fmt.Sprintf("%d", math.MaxInt64), // самое большое число
		"99999999999999999999999999",     // не влезает даже в int64
	} {
		d, err := ParseDur(s)
		if err == nil {
			t.Errorf("ParseDur(%q) вернула %s без ошибки — срок выше Duration", s, d)
		}
		if d < 0 {
			t.Errorf("ParseDur(%q) вернула отрицательный срок %s", s, d)
		}
	}
	// А вот максимально допустимый срок обязан разбираться: проверка не должна
	// отвергать всё подряд, иначе мы просто заменили одну поломку другой.
	if d, err := ParseDur(fmt.Sprintf("%d", maxMinutes)); err != nil {
		t.Errorf("предельный срок %d минут отвергнут: %v", maxMinutes, err)
	} else if d <= 0 {
		t.Errorf("предельный срок разобрался как %s", d)
	}
}

func TestDurJSONRoundTrip(t *testing.T) {
	m := Mission{Objective: "цель", Deadline: Dur(4 * time.Hour), Mode: MissionLongTime}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	// В файле должно быть читаемо, а не «14400000000000».
	if !strings.Contains(string(data), `"4h0m0s"`) {
		t.Errorf("срок должен сериализоваться строкой, получили %s", data)
	}
	var back Mission
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Deadline != Dur(4*time.Hour) {
		t.Errorf("после разбора срок %s, хотели 4h", back.Deadline)
	}
	if back.Objective != "цель" {
		t.Errorf("цель потерялась: %q", back.Objective)
	}
}

func TestDurUnmarshalFromNumber(t *testing.T) {
	// Число в JSON — минуты, как и в ParseDur: старый файл с «30»
	// не должен вдруг означать полчаса микроскопии.
	var m Mission
	if err := json.Unmarshal([]byte(`{"deadline":90}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.Deadline != Dur(90*time.Minute) {
		t.Errorf("число 90 разобрано как %s, хотели 90m", m.Deadline)
	}
	if err := json.Unmarshal([]byte(`{"deadline":null}`), &m); err != nil {
		t.Errorf("null должен означать «не задано»: %v", err)
	}
}

// ---------- Tracker: бюджеты ----------

// tickUntil — прогнать n итераций с заданным ростом токенов.
func tickUntil(t *testing.T, tr *Tracker, n, tokens int) string {
	t.Helper()
	reason := ""
	for i := 0; i < n; i++ {
		reason = tr.Tick(i+1, 0, tokens*(i+1)/max(n, 1))
		if reason != "" {
			break
		}
	}
	return reason
}

func TestTrackerDeadlineStopsRun(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	tr := NewTrackerAt(
		Mission{Mode: MissionLongTime, Deadline: Dur(time.Hour)}.Apply(),
		0, 0, clock,
	)
	// Час работы: 3599 секунд — ещё работаем, 3600 — срок вышел.
	now = now.Add(3599 * time.Second)
	if r := tr.Tick(10, 10, 5000); r != "" {
		t.Errorf("до срока останавливать нельзя, получили %q", r)
	}
	now = now.Add(time.Second)
	if r := tr.Tick(11, 10, 5500); r != StopDeadline {
		t.Errorf("на границе срока ждали %q, получили %q", StopDeadline, r)
	}
	if !tr.Done() {
		t.Error("после остановки трекер должен быть завершён")
	}
	// Ровно на границе срока просрочки ещё нет — есть факт достижения
	// дедлайна. Проверяем её отдельно, сдвинув часы ещё на секунду.
	if tr.Overdue() != 0 {
		t.Errorf("на границе срока просрочки быть не должно, получили %s", tr.Overdue())
	}
	now = now.Add(time.Second)
	if tr.Overdue() < time.Second {
		t.Error("после срока просрочка должна считаться")
	}
}

func TestTrackerCountsOnlyOwnTokens(t *testing.T) {
	// Миссия считает расход от своего старта: прогон на 100k токенов
	// должен запускаться в сессии, где до него уже набежало 300k.
	now := time.Now()
	tr := NewTrackerAt(Mission{Mode: MissionNormal, TokenBudget: 100_000},
		300_000, 0, func() time.Time { return now })
	for i := 1; i <= 5; i++ {
		tr.Tick(i, 1, 300_000+i*100)
	}
	if got := tr.Spent(); got != 500 {
		t.Errorf("расход миссии %d, хотели 500 (только свой)", got)
	}
}

func TestTrackerTokenBudgetStops(t *testing.T) {
	now := time.Now()
	tr := NewTrackerAt(Mission{Mode: MissionNormal, TokenBudget: 1000},
		0, 0, func() time.Time { return now })
	if r := tr.Tick(1, 1, 999); r != "" {
		t.Errorf("до бюджета останавливать нельзя, получили %q", r)
	}
	if r := tr.Tick(2, 1, 1000); r != StopTokens {
		t.Errorf("на границе бюджета ждали %q, получили %q", StopTokens, r)
	}
}

func TestTrackerIterCapStops(t *testing.T) {
	now := time.Now()
	tr := NewTrackerAt(Mission{Mode: MissionNormal, MaxIters: 5}, 0, 0,
		func() time.Time { return now })
	if r := tr.Tick(4, 1, 100); r != "" {
		t.Errorf("до потолка останавливать нельзя, получили %q", r)
	}
	if r := tr.Tick(5, 1, 100); r != StopIters {
		t.Errorf("хотели %q, получили %q", StopIters, r)
	}
}

func TestTrackerToolCallCapStops(t *testing.T) {
	now := time.Now()
	tr := NewTrackerAt(Mission{Mode: MissionNormal, MaxToolCalls: 10}, 0, 0,
		func() time.Time { return now })
	for i := 1; i <= 4; i++ {
		tr.Tick(i, 3, 10) // по 3 вызова за итерацию
	}
	if tr.ToolCalls() != 12 {
		t.Fatalf("вызовов %d, хотели 12", tr.ToolCalls())
	}
	if r := tr.Tick(5, 0, 10); r != StopTools {
		t.Errorf("хотели %q, получили %q", StopTools, r)
	}
}

// TestTrackerStopIsSticky — причина остановки не должна теряться:
// после неё цикл всё равно крутится какое-то время (сбор результата),
// и без «липкой» остановки причина перезаписалась бы на другую.
func TestTrackerStopIsSticky(t *testing.T) {
	now := time.Now()
	tr := NewTrackerAt(Mission{Mode: MissionNormal, TokenBudget: 10}, 0, 0,
		func() time.Time { return now })
	first := tr.Tick(1, 1, 50)
	if first != StopTokens {
		t.Fatalf("хотели %q, получили %q", StopTokens, first)
	}
	// Потом дедлайн «истёк» бы — причина обязана остаться прежней.
	tr.m.Deadline = Dur(time.Nanosecond)
	second := tr.Tick(2, 1, 999)
	if second != StopTokens {
		t.Errorf("причина перезаписалась на %q", second)
	}
	if tr.Stopped() != StopTokens {
		t.Errorf("Stopped() = %q, хотели %q", tr.Stopped(), StopTokens)
	}
}

func TestTrackerNilIsSafe(t *testing.T) {
	// Трекер может быть nil (миссия не задана): методы не должны падать,
	// иначе каждый вызов в цикле требовал бы проверки.
	var tr *Tracker
	if r := tr.Tick(5, 5, 500); r != "" {
		t.Errorf("nil-трекер должен молчать, получил %q", r)
	}
	if tr.Stalled() || tr.Done() || tr.CheckpointDue() || tr.NeedVerify() {
		t.Error("nil-трекер не должен ничего утверждать")
	}
	tr.ChargeTokens(100)
	tr.Progress()
	tr.Stop(StopDone)
	tr.CountCheckpoint()
	_ = tr.Status()
	_ = tr.Left()
}

func TestTrackerCostStopsRun(t *testing.T) {
	now := time.Now()
	// 0.05$ при цене glm-4.6 (0.60/2.20 за миллион) — это около
	// 60k входных + 15k выходных токенов.
	tr := NewTrackerAt(Mission{Mode: MissionNormal, CostBudget: 0.05},
		0, 0, func() time.Time { return now })
	tr.WithModel("zai", "glm-4.6")
	reason := ""
	for i := 1; i <= 100 && reason == ""; i++ {
		tr.ChargeTokens(i * 1000)
		reason = tr.Tick(i, 1, tr.Spent())
	}
	if reason != StopCost {
		t.Errorf("бюджет денег не сработал: %q (потрачено %d токенов, $%.4f)",
			reason, tr.Spent(), tr.Cost("", ""))
	}
}

// TestTrackerCostWithoutModelNeverStops — честная картина: без модели
// цену не знаем, потратить бюджет нечем. Молча срабатывающий «бюджет
// денег», который ничего не считает, хуже явного нуля.
func TestTrackerCostWithoutModelNeverStops(t *testing.T) {
	now := time.Now()
	tr := NewTrackerAt(Mission{Mode: MissionNormal, CostBudget: 0.01}, 0, 0,
		func() time.Time { return now })
	tr.ChargeTokens(10_000_000)
	if r := tr.Tick(1, 1, 10_000_000); r != "" {
		t.Errorf("без модели бюджет денег не должен срабатывать, получили %q", r)
	}
	if c := tr.Cost("", ""); c != 0 {
		t.Errorf("стоимость без модели должна быть 0, получили %v", c)
	}
}

func TestTrackerCostUnknownPriceIsNotSpent(t *testing.T) {
	now := time.Now()
	tr := NewTrackerAt(Mission{Mode: MissionNormal, CostBudget: 0.01}, 0, 0,
		func() time.Time { return now })
	tr.ChargeTokens(1_000_000)
	// Неизвестная цена: показывать выдуманную сумму хуже, чем ноль.
	if c := tr.Cost("какой-то-вендор", "неизвестная-модель"); c != 0 {
		t.Errorf("при неизвестной цене стоимость должна быть 0, получили %v", c)
	}
	if r := tr.Tick(1, 1, 1_000_000); r != "" {
		t.Errorf("при неизвестной цене бюджет денег не должен срабатывать, получили %q", r)
	}
}

// ---------- Критерии приёмки ----------

func TestTrackerNeedsVerifyWhenAcceptanceSet(t *testing.T) {
	now := time.Now()
	tr := NewTrackerAt(Mission{Mode: MissionNormal, Acceptance: []string{"тесты зелёные"}},
		0, 0, func() time.Time { return now })
	if !tr.NeedVerify() {
		t.Fatal("с критериями приёмки работа не может считаться законченной без проверки")
	}
	tr.AskVerify()
	tr.AskVerify()
	tr.AskVerify()
	// Больше не требуем: агент, честно не способный выполнить критерий,
	// иначе сожжёт весь бюджет требованиями.
	if tr.NeedVerify() {
		t.Error("требовать проверку бесконечно нельзя")
	}
}

func TestTrackerNoVerifyWithoutAcceptance(t *testing.T) {
	now := time.Now()
	tr := NewTrackerAt(Mission{Mode: MissionNormal}, 0, 0,
		func() time.Time { return now })
	if tr.NeedVerify() {
		t.Error("без критериев приёмки требование проверки неуместно")
	}
}

func TestTrackerNoVerifyAfterStop(t *testing.T) {
	now := time.Now()
	tr := NewTrackerAt(Mission{Mode: MissionNormal, Acceptance: []string{"x"}},
		0, 0, func() time.Time { return now })
	tr.Stop(StopDeadline)
	if tr.NeedVerify() {
		t.Error("после остановки требовать проверку нельзя")
	}
}

// ---------- Застой ----------

func TestTrackerStalledDetected(t *testing.T) {
	now := time.Now()
	tr := NewTrackerAt(Mission{Mode: MissionNormal, StallLimit: 3}, 0, 0,
		func() time.Time { return now })
	for i := 1; i <= 2; i++ {
		tr.Tick(i, 1, 100)
		if tr.Stalled() {
			t.Fatalf("застой на итерации %d, хотя прошло лишь %d", i, i)
		}
	}
	tr.Tick(3, 1, 100)
	if !tr.Stalled() {
		t.Error("через StallLimit итераций без прогресса должен быть застой")
	}
	// Прогресс снимает застой: вернулись к работе — и можно работать дальше.
	tr.Progress()
	if tr.Stalled() {
		t.Error("после прогресса застоя быть не должно")
	}
}

func TestTrackerStallDisabledByNegative(t *testing.T) {
	now := time.Now()
	tr := NewTrackerAt(Mission{Mode: MissionNormal, StallLimit: -1}, 0, 0,
		func() time.Time { return now })
	for i := 1; i <= 50; i++ {
		tr.Tick(i, 1, 100)
	}
	if tr.Stalled() {
		t.Error("отрицательный StallLimit должен отключать проверку застоя")
	}
}

// ---------- Чекпоинты ----------

func TestTrackerCheckpointDue(t *testing.T) {
	now := time.Now()
	tr := NewTrackerAt(Mission{Mode: MissionLongTime}, 0, 0,
		func() time.Time { return now })
	every := tr.Mission().CheckpointEvery
	if every <= 0 {
		t.Fatalf("у long-time должен быть интервал сохранения, получили %d", every)
	}
	if tr.CheckpointDue() {
		t.Error("до первой итерации сохранять нечего")
	}
	tr.Tick(every-1, 1, 10)
	if tr.CheckpointDue() {
		t.Errorf("на итерации %d сохранять рано", every-1)
	}
	tr.Tick(every, 1, 10)
	if !tr.CheckpointDue() {
		t.Errorf("на итерации %d сохранять пора", every)
	}
	tr.CountCheckpoint()
	if tr.Checkpoints() != 1 {
		t.Errorf("сохранений %d, хотели 1", tr.Checkpoints())
	}
}

// ---------- Файл миссии ----------

func TestSaveAndLoadMission(t *testing.T) {
	work := t.TempDir()
	m := Mission{
		Objective: "починить тесты",
		Mode:      MissionLongTime,
		Deadline:  Dur(2 * time.Hour),
		Acceptance: []string{
			"go test ./... зелёные",
			"vet молчит",
		},
		TokenBudget: 250_000,
	}
	name, err := SaveMission(work, m)
	if err != nil {
		t.Fatal(err)
	}
	got, from, err := LoadMission(work)
	if err != nil {
		t.Fatal(err)
	}
	if from != name {
		t.Errorf("файл %s, а прочитан %s", name, from)
	}
	if got.Objective != m.Objective {
		t.Errorf("цель %q, хотели %q", got.Objective, m.Objective)
	}
	if got.Deadline != m.Deadline {
		t.Errorf("срок %s, хотели %s", got.Deadline, m.Deadline)
	}
	if len(got.Acceptance) != 2 || got.Acceptance[0] != m.Acceptance[0] {
		t.Errorf("критерии потерялись: %v", got.Acceptance)
	}
	if got.TokenBudget != m.TokenBudget {
		t.Errorf("бюджет %d, хотели %d", got.TokenBudget, m.TokenBudget)
	}
	// Файл обязан быть читаемым: срок строкой, а не наносекундами.
	body, _ := os.ReadFile(name)
	if !strings.Contains(string(body), "2h0m0s") {
		t.Errorf("в файле срок не читаем:\n%s", body)
	}
}

func TestLoadMissionAbsentIsNormal(t *testing.T) {
	// Отсутствие файла — обычный ход, а не поломка: без него gcli
	// работает ровно как раньше.
	m, _, err := LoadMission(t.TempDir())
	if err != nil {
		t.Fatalf("отсутствие mission.json не должно быть ошибкой: %v", err)
	}
	if m.Mode != MissionNormal || m.Deadline != 0 {
		t.Errorf("без файла должен быть обычный ход, получили %+v", m)
	}
}

func TestLoadMissionBrokenIsError(t *testing.T) {
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(MissionPath(work)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(MissionPath(work), []byte(`{ "mode": `), 0o644); err != nil {
		t.Fatal(err)
	}
	// Молча откатиться к обычному режиму нельзя: человек собрал
	// четырёхчасовой прогон, а получил 20 минут и не узнал бы.
	if _, _, err := LoadMission(work); err == nil {
		t.Fatal("битый mission.json должен быть ошибкой, а не обычным режимом")
	}
}

func TestLoadMissionRejectsBadMode(t *testing.T) {
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(MissionPath(work)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(MissionPath(work), []byte(`{"mode":"вечность"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadMission(work)
	if err == nil {
		t.Fatal("неизвестный режим в файле должен отвергаться")
	}
	if !strings.Contains(err.Error(), "long-time") {
		t.Errorf("в ошибке нет списка режимов: %v", err)
	}
}

func TestMissionTemplateHasComments(t *testing.T) {
	// Пустой файл бесполезен: человек должен понять синтаксис,
	// не читая документацию.
	for _, want := range []string{"mode", "acceptance", "deadline", "long-time"} {
		if !strings.Contains(MissionTemplate, want) {
			t.Errorf("в заготовке mission.json нет %q", want)
		}
	}
}

// ---------- Промпт ----------

func TestMissionPromptBlock(t *testing.T) {
	m := Mission{
		Objective:  "починить тесты",
		Mode:       MissionLongTime,
		Acceptance: []string{"go test ./... зелёные"},
	}.Apply()
	block := m.PromptBlock()
	if block == "" {
		t.Fatal("миссия обязана что-то говорить модели")
	}
	for _, want := range []string{
		"починить тесты", "go test ./... зелёные", "ПРОВЕРЬ", "4h",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("в промпте миссии нет %q:\n%s", want, block)
		}
	}
}

func TestMissionPromptBlockEmptyForPlainTurn(t *testing.T) {
	// Обычный ход не должен получать блок про автономность: лишний
	// текст в промпте жжёт токены в каждой сессии.
	if b := (Mission{Mode: MissionNormal}).Apply().PromptBlock(); b != "" {
		t.Errorf("обычный ход не должен получать блок миссии, получил:\n%s", b)
	}
}

// ---------- Отображение ----------

func TestFormatDur(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{45 * time.Second, "45с"},
		{30 * time.Minute, "30м"},
		{90 * time.Second, "1м 30с"},
		{2 * time.Hour, "2ч"},
		{150 * time.Minute, "2ч 30м"},
		{-30 * time.Minute, "30м"},
	}
	for _, c := range cases {
		if got := FormatDur(c.in); got != c.want {
			t.Errorf("FormatDur(%s) = %q, хотели %q", c.in, got, c.want)
		}
	}
}

func TestTrackerStatusMentionsLimits(t *testing.T) {
	now := time.Now()
	tr := NewTrackerAt(Mission{Mode: MissionLongTime, TokenBudget: 500_000},
		0, 0, func() time.Time { return now })
	tr.Tick(10, 5, 100_000)
	s := tr.Status()
	for _, want := range []string{"время", "итерации", "токены"} {
		if !strings.Contains(s, want) {
			t.Errorf("в строке состояния нет %q: %q", want, s)
		}
	}
}

func TestMissionSummary(t *testing.T) {
	m := Mission{
		Objective:   "перевести проект на новый API",
		Mode:        MissionLongTime,
		TokenBudget: 250_000,
	}.Apply()
	s := m.Summary()
	if !strings.Contains(s, "long-time") || !strings.Contains(s, "перевести") {
		t.Errorf("сводка задания бедна: %q", s)
	}
}
