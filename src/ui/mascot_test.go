package ui

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// mascotUI — UI с включённым маскотом и отключённым цветом.
func mascotUI() (*UI, *strings.Builder) {
	buf := &strings.Builder{}
	u := New(Options{
		Theme:      "ember",
		Unicode:    true,
		Color:      false,
		Animations: true,
		Width:      60,
		Out:        buf,
	})
	u.SetMascot(true)
	return u, buf
}

// testStates — все состояния от первого до последнего.
func testStates() []MascotState {
	out := make([]MascotState, 0, int(mascotLast)+1)
	for s := MascotIdle; s <= mascotLast; s++ {
		out = append(out, s)
	}
	return out
}

// У всех 19 состояний должны быть кадры, и каждый кадр — ровно три
// строки референса: уши, мордочка в скобках, лапы «> ^ <».
func TestMascotIsTheReferenceCat(t *testing.T) {
	u, _ := mascotUI()
	for _, s := range testStates() {
		frames := u.MascotFrames(s)
		if len(frames) == 0 {
			t.Fatalf("состояние %d без кадров", s)
		}
		for i, f := range frames {
			lines := catLines(f)
			if len(lines) != catHeight {
				t.Fatalf("состояние %d кадр %d: %d строк, ожидалось %d", s, i, len(lines), catHeight)
			}
			if !strings.HasPrefix(lines[0], "  /\\_/\\") {
				t.Errorf("состояние %d кадр %d: нет ушей: %q", s, i, lines[0])
			}
			if !strings.HasPrefix(lines[1], " ( ") || !strings.Contains(lines[1], " )") {
				t.Errorf("состояние %d кадр %d: мордочка не в скобках: %q", s, i, lines[1])
			}
			if !strings.HasPrefix(lines[2], "  > ^ <") {
				t.Errorf("состояние %d кадр %d: нет лап: %q", s, i, lines[2])
			}
			// Тело мордочки добивается до catWidth: анимация без сдвигов.
			if n := runeLen(strings.TrimRight(lines[0], " ")); n > catWidth {
				t.Errorf("состояние %d кадр %d: строка ушей шире тела: %q", s, i, lines[0])
			}
		}
	}
}

// Лица состояний совпадают с референсом проекта.
func TestMascotFacesMatchReference(t *testing.T) {
	u, _ := mascotUI()
	cases := []struct {
		s    MascotState
		face string
		suf  string
	}{
		{MascotIdle, "^.^", ""},
		{MascotThink, "-.-", ""},
		{MascotWork, ">.<", ""},
		{MascotHappy, "^ω^", ""},
		{MascotOops, "x_x", ""},
		{MascotSleep, "-.-", " zZ"},
		{MascotListen, "°.°", ""},
		{MascotSurprised, "°□°", ""},
		{MascotBored, "-.-", ""},
		{MascotEat, "~.~", ""},
		{MascotBlink, "-.-", ""},
		{MascotWink, "^.~", ""},
		{MascotContent, "^_^", ""},
		{MascotShy, "^//^", ""},
		{MascotCry, "T_T", ""},
		{MascotAngry, "Ò_Ó", ""},
		{MascotPensive, "-.-", "?"},
		{MascotScared, "°△°", ""},
		{MascotPurr, "^~^", ""},
	}
	for _, c := range cases {
		f := u.mascotFrameOf(c.s, 0)
		if f.face != c.face || f.suffix != c.suf {
			t.Errorf("состояние %d: лицо %q+%q, ожидалось %q+%q", c.s, f.face, f.suffix, c.face, c.suf)
		}
		lines := catLines(f)
		if !strings.Contains(lines[1], "( "+c.face+" )") {
			t.Errorf("состояние %d: в строке мордочки нет %q: %q", c.s, c.face, lines[1])
		}
	}
}

// Анимированные состояния живут: у idle есть моргание, у сна — zZ,
// у радости — смена кадров, у задумчивости — знак вопроса.
func TestMascotAnimatedStates(t *testing.T) {
	u, _ := mascotUI()
	if got := len(u.MascotFrames(MascotIdle)); got < 3 {
		t.Errorf("у простоя %d кадров, нужно ≥3 (покой/морг/покой)", got)
	}
	for _, f := range u.MascotFrames(MascotSleep) {
		if !strings.Contains(catLines(f)[1], "zZ") && !strings.Contains(catLines(f)[1], "Zz") {
			t.Errorf("кадр сна без zZ: %q", catLines(f)[1])
		}
	}
	happy := u.MascotFrames(MascotHappy)
	if len(happy) < 2 || happy[0].face == happy[1].face {
		t.Error("радость должна анимироваться сменой лиц")
	}
	if p := u.MascotFrames(MascotPensive); !strings.HasSuffix(catLines(p[0])[1], "?") {
		t.Errorf("задумчивость должна нести знак вопроса: %q", catLines(p[0])[1])
	}
}

// ASCII-версия: только латиница, цифры и знаки препинания — никаких
// юникод-кракозябр в терминалах без псевдографики.
func TestMascotASCIIFaces(t *testing.T) {
	u, _ := emberASCIIUI()
	for _, s := range testStates() {
		for i, f := range u.MascotFrames(s) {
			for _, line := range catLines(f) {
				for _, r := range line {
					if r > 127 {
						t.Errorf("ASCII-кадр состояния %d кадр %d содержит не-ASCII %q: %q", s, i, r, line)
						break
					}
				}
			}
		}
	}
	// Ключевые соответствия референсу.
	if f := u.mascotFrameOf(MascotHappy, 0); f.face != "^w^" {
		t.Errorf("ASCII-радость = %q, ожидалось ^w^", f.face)
	}
	if f := u.mascotFrameOf(MascotSurprised, 0); f.face != "O.O" {
		t.Errorf("ASCII-удивление = %q, ожидалось O.O", f.face)
	}
	if f := u.mascotFrameOf(MascotListen, 0); f.face != "o.o" {
		t.Errorf("ASCII-слушание = %q, ожидалось o.o", f.face)
	}
}

// Рендер состояния: без цвета буфер должен содержать мордочку.
func TestMascotRender(t *testing.T) {
	u, _ := mascotUI()
	out := StripANSI(u.MascotRender(MascotWork, 0))
	if !strings.Contains(out, "( >.< )") {
		t.Errorf("рабочий кадр должен показывать ( >.< ):\n%s", out)
	}
}

// Морда-однострочник для спиннера и реплик: узнаётся и короткая.
func TestMascotInline(t *testing.T) {
	u, _ := mascotUI()
	if got := StripANSI(u.mascotInline(MascotThink, 0)); got != "(-.-)" {
		t.Errorf("однострочник думающего кота = %q, ожидалось (-.-)", got)
	}
	if got := StripANSI(u.mascotInline(MascotWork, 0)); got != "(>.<)" {
		t.Errorf("однострочник работающего кота = %q, ожидалось (>.<)", got)
	}
}

// Перелив: без 256 цветов слово статично, с ними — волна меняет кадры.
func TestShimmerWord(t *testing.T) {
	u, _ := mascotUI()
	if got := u.shimmerWord("thinking...", 0); got != "thinking..." {
		t.Errorf("без цвета перелив должен вернуть слово как есть: %q", got)
	}

	buf := &strings.Builder{}
	u2 := New(Options{Theme: "ember", Unicode: true, Color: true, Grade: ColorRGB, Width: 60, Out: buf})
	w0 := u2.shimmerWord("thinking...", 0)
	w1 := u2.shimmerWord("thinking...", 0.35)
	// Каждый символ окрашен отдельно, поэтому «thinking» ищем
	// в тексте без ANSI, а коды волны — в сыром выводе.
	if !strings.Contains(StripANSI(w0), "thinking") || !strings.Contains(w0, "\033[") {
		t.Fatalf("перелив должен раскрашивать слово: %q", w0)
	}
	if w0 == w1 {
		t.Error("волна должна двигаться: кадры в разные моменты различаются")
	}
	if n := runeLen(StripANSI(w0)); n != runeLen("thinking...") {
		t.Errorf("перелив изменил длину слова: %d", n)
	}
}

// У кошки один пост — над строкой состояния. Баннер кота не рисует:
// раньше на первом экране было два одинаковых кота подряд.
func TestMascotPerch(t *testing.T) {
	u, buf := mascotUI()
	u.MascotPerch()
	out := StripANSI(buf.String())
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != catHeight {
		t.Fatalf("кот у строки состояния занял %d строк, ждём %d:\n%s", len(lines), catHeight, out)
	}
	if !strings.Contains(out, "/\\_/\\") {
		t.Errorf("кот потерял уши:\n%s", out)
	}
	// Без маскота — тишина.
	u2, buf2 := mascotUI()
	u2.SetMascot(false)
	u2.MascotPerch()
	if buf2.String() != "" {
		t.Errorf("выключенный маскот всё равно печатается: %q", buf2.String())
	}
}

// idleMood — хронология простоя: бодрствует со морганием, потом
// скучает, после долгой паузы засыпает по-настоящему.
func TestIdleMood(t *testing.T) {
	u, _ := mascotUI()
	if s, _ := idleMood(2 * time.Second); s != MascotIdle && s != MascotBlink {
		t.Errorf("2 секунды простоя: состояние %d, ждём покой или морг", s)
	}
	if s, _ := idleMood(12 * time.Second); s != MascotBored {
		t.Errorf("12 секунд простоя: состояние %d, ждём MascotBored", s)
	}
	if s, _ := idleMood(30 * time.Second); s != MascotSleep {
		t.Errorf("30 секунд простоя: состояние %d, ждём MascotSleep", s)
	}
	// Кадры всех состояний простоя валидны.
	for _, st := range []MascotState{MascotIdle, MascotBlink, MascotBored, MascotSleep} {
		n := len(u.MascotFrames(st))
		for i := 0; i < 6; i++ {
			s, f := idleMood(time.Duration(30+i) * time.Second)
			_ = s
			if f < 0 || f >= n && st == MascotSleep {
				t.Errorf("кадр сна %d вне диапазона 0..%d", f, n-1)
			}
		}
	}
}

// syncBuf — потокобезопасный буфер: фоновая анимация пишет в него из
// своей горутины, тест читает из своей.
type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *syncBuf) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Len()
}

// Фоновая жизнь: старт/стоп без гонок, кадры реально рисуются,
// после остановки вывод прекращается.
func TestMascotIdleStartStop(t *testing.T) {
	buf := &syncBuf{}
	u := New(Options{
		Theme:      "ember",
		Unicode:    true,
		Color:      false,
		Animations: true,
		Width:      60,
		Out:        buf,
	})
	u.SetMascot(true)
	u.MascotIdleStart()
	u.MascotIdleStart() // повторный старт — тихий no-op
	time.Sleep(2 * time.Second)
	if buf.Len() == 0 {
		t.Errorf("фоновая анимация не нарисовала ни одного кадра")
	}
	// Каждый кадр — атомарная перерисовка: сохранение и возврат курсора.
	for _, frame := range strings.Split(buf.String(), "\0337") {
		if frame != "" && !strings.Contains(frame, "\0338") {
			t.Errorf("кадр без восстановления курсора: %q", frame)
		}
	}
	u.MascotIdleStop()
	u.MascotIdleStop() // повторный стоп — тоже
	after := buf.Len()
	time.Sleep(150 * time.Millisecond)
	if buf.Len() != after {
		t.Errorf("после остановки фоновая анимация продолжила писать")
	}
	// На выключенном маскоте ничего не пишется.
	u.SetMascot(false)
	u.MascotIdleStart()
	time.Sleep(120 * time.Millisecond)
	if buf.Len() != after {
		t.Errorf("выключенный маскот запустил фоновую анимацию")
	}
}

// mascotRedraw — атомарный кадр с сохранением и восстановлением курсора.
func TestMascotRedrawAtomic(t *testing.T) {
	buf := &syncBuf{}
	u := New(Options{
		Theme:      "ember",
		Unicode:    true,
		Color:      false,
		Animations: true,
		Width:      60,
		Out:        buf,
	})
	u.SetMascot(true)
	u.mu.Lock()
	u.mascotRedraw(MascotThink, 0)
	u.mu.Unlock()
	out := buf.String()
	if !strings.Contains(out, "\0337") || !strings.Contains(out, "\0338") {
		t.Errorf("перерисовка должна сохранять и восстанавливать курсор: %q", out)
	}
	// Без цвета кадр — чистый текст: лицо и уши ищем напрямую
	// (StripANSI здесь не годится: он глотает всё до ближайшего «m»).
	if !strings.Contains(out, "-.-") || !strings.Contains(out, "/\\_/\\") {
		t.Errorf("поза «думаю» не нарисована: %q", out)
	}
	if n := strings.Count(out, "\033[4A"); n != 1 {
		t.Errorf("перерисовка должна подниматься ровно на %d строк (кот+статус), кадров: %d", mascotRedrawTop, n)
	}
}

// Привязка к строке приглашения: любая печать её сбрасывает, усадка —
// поднимает. Пока привязки нет, фоновая анимация не трогает экран —
// именно слепые перерисовки портили статус и оставляли двойные лапы.
func TestMascotAnchorGuard(t *testing.T) {
	buf := &syncBuf{}
	u := New(Options{
		Theme:      "ember",
		Unicode:    true,
		Color:      false,
		Animations: true,
		Width:      60,
		Out:        buf,
	})
	u.SetMascot(true)

	// Усадка поднимает привязку и обнуляет простой.
	u.MascotIdleStart()
	u.mu.Lock()
	anchored := u.mascotAnchored
	idleSince := u.idleSince
	u.mu.Unlock()
	if !anchored {
		t.Fatal("после усадки привязка должна быть поднята")
	}
	if time.Since(idleSince) > time.Second {
		t.Errorf("усадка не сбросила таймер простоя: %v", time.Since(idleSince))
	}

	// Пока привязка поднята — кадры рисуются.
	deadline := time.Now().Add(2 * time.Second)
	for buf.Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if buf.Len() == 0 {
		t.Fatal("фоновая анимация не рисует при поднятой привязке")
	}

	// Любая печать сбрасывает привязку — кадры замирают.
	u.Info("проверка привязки")
	u.mu.Lock()
	anchored = u.mascotAnchored
	u.mu.Unlock()
	if anchored {
		t.Fatal("печать должна сбрасывать привязку маскота")
	}
	after := buf.Len()
	time.Sleep(1200 * time.Millisecond)
	if buf.Len() != after {
		t.Errorf("без привязки анимация продолжила рисовать: было %d, стало %d", after, buf.Len())
	}

	// Повторная усадка снова поднимает привязку.
	u.MascotIdleStart()
	u.mu.Lock()
	anchored = u.mascotAnchored
	u.mu.Unlock()
	if !anchored {
		t.Fatal("повторная усадка должна поднимать привязку")
	}
	u.MascotIdleStop()
}

// Спиннер с мордочкой кота: в режиме без цвета ничего не пишет.
func TestSpinnerMascotNoopWithoutColor(t *testing.T) {
	u, buf := mascotUI()
	u.opts.Color = false
	u.SetSpinnerMascot(MascotWork)
	u.SpinnerStart("работа")
	u.SpinnerStop()
	if buf.String() != "" {
		t.Errorf("без цвета спиннер не должен ничего писать: %q", buf.String())
	}
}
