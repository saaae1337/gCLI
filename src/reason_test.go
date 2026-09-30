package main

import (
	"strings"
	"testing"
	"time"

	"gcli/ui"
)

// Тесты на слой размышлений: по умолчанию они копятся молча и показываются
// только по Ctrl+O. Проверяем состояние напрямую (не через терминал):
// буфер, включённость показа и то, что back-to-thought не дублируется.

// reasonApp — приложение с собранным UI и пустым буфером размышлений.
//
// Цвет и анимации выключены намеренно: тесты проверяют логику показа, а не
// анимацию, и с переливами в буфер падала бы гонка с горутиной кадра.
func reasonApp(t *testing.T) (*app, *strings.Builder) {
	t.Helper()
	buf := &strings.Builder{}
	a := &app{}
	a.ui = ui.New(ui.Options{
		Theme: "ember", Unicode: true, Color: false, Grade: ui.Color16,
		Animations: false, Width: 80, Out: buf,
	})
	return a, buf
}

// Размышления по умолчанию не показываются: копятся в буфере и молчат.
func TestReasonHiddenByDefault(t *testing.T) {
	a, buf := reasonApp(t)
	a.onReason("мысль один ")
	a.onReason("мысль два")

	if got := a.reasonText(); got != "мысль один мысль два" {
		t.Errorf("буфер размышлений = %q", got)
	}
	if out := buf.String(); strings.Contains(out, "мысль") {
		t.Errorf("размышления напечатаны без Ctrl+O:\n%q", out)
	}
	if !a.reasonAny {
		t.Error("reasonAny должен быть выставлен: модель присылала размышления")
	}
}

// Подсказка про Ctrl+O появляется на строке ожидания один раз за ход.
func TestReasonHintAppearsOnce(t *testing.T) {
	a, _ := reasonApp(t)
	a.onReason("первая")
	if a.ui.SpinHint() != reasonHint {
		t.Errorf("после первого куска подсказка должна стоять на строке ожидания, получено %q", a.ui.SpinHint())
	}
	a.reasonMu.Lock()
	shown := a.reasonShown
	a.reasonMu.Unlock()
	if shown {
		t.Error("reasonShown не должен выставляться до Ctrl+O")
	}
}

// Ctrl+O показывает накопленное задним числом — и ровно один раз.
func TestToggleReasonShowsBuffer(t *testing.T) {
	a, buf := reasonApp(t)
	a.onReason("накопленная мысль")
	buf.Reset()

	a.toggleReason()

	out := buf.String()
	if !strings.Contains(out, "накопленная мысль") {
		t.Errorf("Ctrl+O должен показать накопленные размышления:\n%q", out)
	}
	if !strings.Contains(out, "размышления") {
		t.Errorf("нет маркера потока:\n%q", out)
	}
	if n := strings.Count(out, "размышления"); n != 1 {
		t.Errorf("маркер потока напечатан %d раз, ожидался один:\n%q", n, out)
	}

	// Повторное нажатие сворачивает, а не дублирует.
	buf.Reset()
	a.toggleReason()
	if got := buf.String(); strings.Contains(got, "накопленная мысль") {
		t.Errorf("повторный Ctrl+O напечатал тот же блок снова:\n%q", got)
	}
	if !strings.Contains(buf.String(), "\n") {
		t.Errorf("сворачивание обязано закрыть строку, иначе ответ слипнется с мыслью:\n%q", buf.String())
	}
}

// Два Ctrl+O подряд: первый открывает поток и включает показ, второй
// сворачивает. Баг прошлой версии — повторное нажатие приводило
// reasonLive/reasonHidden к одному и тому же значению, и поток не закрывался.
func TestTwoCtrlOTogglesOpenAndClose(t *testing.T) {
	a, buf := reasonApp(t)
	a.onReason("накоплено")

	a.toggleReason() // показать
	a.reasonMu.Lock()
	live, open := a.reasonLive, a.reasonOpen
	a.reasonMu.Unlock()
	if !live || !open {
		t.Fatalf("после первого Ctrl+O ждём live=true open=true, получено live=%v open=%v", live, open)
	}

	buf.Reset()
	a.toggleReason() // свернуть
	a.reasonMu.Lock()
	live, open = a.reasonLive, a.reasonOpen
	a.reasonMu.Unlock()
	if live || open {
		t.Errorf("после второго Ctrl+O ждём live=false open=false, получено live=%v open=%v", live, open)
	}
	if got := buf.String(); strings.Contains(got, "накоплено") {
		t.Errorf("второй Ctrl+O не должен печатать текст заново:\n%q", got)
	}

	// Третий Ctrl+O открывает снова и печатает только невыведенное: то,
	// что уже было на экране до сворачивания, дублировать нельзя.
	a.onReason(" и продолжение")
	buf.Reset()
	a.toggleReason()
	got := buf.String()
	if !strings.Contains(got, "и продолжение") {
		t.Errorf("третий Ctrl+O должен показать невыведенный остаток:\n%q", got)
	}
	if strings.Contains(got, "накоплено") {
		t.Errorf("уже показанный кусок не должен печататься заново:\n%q", got)
	}
}

// После показа следующие куски идут на экран сами (поток открыт).
func TestReasonLiveAfterToggle(t *testing.T) {
	a, buf := reasonApp(t)
	a.onReason("первое")
	a.toggleReason()
	buf.Reset()

	a.onReason("второе")
	if !strings.Contains(buf.String(), "второе") {
		t.Errorf("после Ctrl+O куски должны печататься сразу:\n%q", buf.String())
	}
}

// Свёрнутые размышления снова копятся молча, а не текут на экран.
func TestReasonHiddenAgainKeepsBuffering(t *testing.T) {
	a, buf := reasonApp(t)
	a.onReason("первое")
	a.toggleReason() // показать
	a.toggleReason() // свернуть
	buf.Reset()

	a.onReason("второе")
	if strings.Contains(buf.String(), "второе") {
		t.Errorf("после сворачивания размышления не должны печататься:\n%q", buf.String())
	}
	if !strings.Contains(a.reasonText(), "второе") {
		t.Error("размышления должны копиться дальше, даже когда свёрнуты")
	}
	if a.reasonShownAll() {
		t.Error("reasonShownAll врёт: скрытый кусок на экране не появлялся")
	}
}

// Ctrl+O до первого куска не должен ломать ход: воля пользователя
// сохраняется, и мысли печатаются, как только они придут.
func TestToggleBeforeFirstChunk(t *testing.T) {
	a, buf := reasonApp(t)
	buf.Reset()

	a.toggleReason()
	if !strings.Contains(buf.String(), "размышлений пока нет") {
		t.Errorf("нужно честно сказать, что показывать нечего:\n%q", buf.String())
	}
	if !a.reasonLive {
		t.Error("нажатие до первого куска должно включать показ: человек явно ждёт мыслей")
	}

	buf.Reset()
	a.onReason("первая мысль")
	if !strings.Contains(buf.String(), "первая мысль") {
		t.Errorf("после раннего Ctrl+O мысли должны идти сразу:\n%q", buf.String())
	}
}

// Ответ модели переводит строку: иначе текст ответа допишется в хвост мысли.
func TestCloseThinkStreamEndsStream(t *testing.T) {
	a, buf := reasonApp(t)
	a.onReason("мысль")
	a.toggleReason() // открыть поток
	buf.Reset()

	a.closeThinkStream()

	if strings.Contains(buf.String(), "мысль") {
		t.Errorf("закрытие потока не должно допечатывать мысль:\n%q", buf.String())
	}
	a.reasonMu.Lock()
	open := a.reasonOpen
	a.reasonMu.Unlock()
	if open {
		t.Error("поток должен быть закрыт после ответа")
	}

	// Показанная мысль остаётся показанной: закрытие потока не стирает
	// её с экрана, и отметка «показано» обязана это помнить.
	if !a.reasonShownAll() {
		t.Error("reasonShownAll врёт: мысль была показана до закрытия потока")
	}
}

// Новый ход обнуляет буфер, но уважает выбор пользователя: включённый показ
// размышлений переживает смену ходов.
func TestResetReasonKeepsUserChoice(t *testing.T) {
	a, _ := reasonApp(t)
	a.onReason("старое")
	a.toggleReason() // пользователь включил показ
	a.resetReason()

	if got := a.reasonText(); got != "" {
		t.Errorf("в новом ходу буфер должен быть пуст, получено %q", got)
	}
	a.reasonMu.Lock()
	live, hinted, any := a.reasonLive, a.reasonHinted, a.reasonAny
	a.reasonMu.Unlock()
	if !live {
		t.Error("включённый показ размышлений должен пережить новый ход")
	}
	if hinted || any {
		t.Error("состояние хода (подсказка, факт размышлений) должно обнуляться")
	}
}

// finishReason отдаёт мысли хода в reasonLast: Ctrl+O между ходами должен
// показать именно их, а не отвечать «размышлений пока нет».
func TestFinishReasonKeepsLastBlock(t *testing.T) {
	a, _ := reasonApp(t)
	a.onReason("мысли хода")
	a.finishReason()

	a.reasonMu.Lock()
	last := a.reasonLast
	a.reasonMu.Unlock()
	if last != "мысли хода" {
		t.Errorf("reasonLast = %q, ждали мысли хода", last)
	}
}

// Ход без размышлений (быстрый ответ, вызов инструмента) не стирает прошлые
// мысли: они всё ещё последние, что есть.
func TestFinishReasonWithoutThoughtsKeepsPrevious(t *testing.T) {
	a, _ := reasonApp(t)
	a.onReason("прошлые мысли")
	a.finishReason()

	a.resetReason() // новый ход, модель не размышляла
	a.finishReason()

	a.reasonMu.Lock()
	last := a.reasonLast
	a.reasonMu.Unlock()
	if last != "прошлые мысли" {
		t.Errorf("reasonLast = %q, прошлые мысли должны были уцелеть", last)
	}
}

// Между ходами Ctrl+O показывает мысли последнего хода — тот самый баг,
// из-за которого клавиша отвечала «размышлений пока нет».
func TestCtrlOBetweenTurnsShowsLastReason(t *testing.T) {
	a, buf := reasonApp(t)
	a.onReason("мысли прошлого хода")
	a.finishReason()
	buf.Reset()

	a.ctrlO() // running == false → путь между ходами

	out := buf.String()
	if !strings.Contains(out, "мысли прошлого хода") {
		t.Errorf("Ctrl+O между ходами должен показать мысли последнего хода:\n%q", out)
	}
	if strings.Contains(out, "размышлений пока нет") {
		t.Errorf("при непустом reasonLast отвечать «пока нет» нельзя:\n%q", out)
	}
	if !a.reasonPastShown() {
		t.Error("после показа мыслей REPL должен перерисовать приглашение (reasonPast)")
	}

	// Повторное нажатие печатает заново: блок уехал вверх по экрану.
	buf.Reset()
	a.ctrlO()
	if !strings.Contains(buf.String(), "мысли прошлого хода") {
		t.Errorf("повторный Ctrl+O должен напечатать мысли снова:\n%q", buf.String())
	}
}

// До первого хода показывать нечего — но воля пользователя сохраняется.
func TestCtrlOBetweenTurnsWithoutAnyReason(t *testing.T) {
	a, buf := reasonApp(t)
	a.ctrlO()

	if !strings.Contains(buf.String(), "размышлений пока нет") {
		t.Errorf("нужно честно сказать, что показывать нечего:\n%q", buf.String())
	}
	if a.reasonPastShown() {
		t.Error("пустой блок не требует перерисовки приглашения")
	}
}

// ctrlO разводит ход и промежуток по a.running: во время хода работает
// toggleReason (мысли текущего хода), после — showLastReason.
func TestCtrlOBranchesOnRunning(t *testing.T) {
	a, buf := reasonApp(t)
	a.resetReason()
	a.onReason("текущий ход")

	a.mu.Lock()
	a.running = true
	a.mu.Unlock()
	buf.Reset()
	a.ctrlO()
	out := buf.String()
	if !strings.Contains(out, "текущий ход") {
		t.Errorf("во время хода Ctrl+O должен показать мысли текущего хода:\n%q", out)
	}
	if strings.Contains(out, "размышлений пока нет") {
		t.Errorf("во время хода отвечать «пока нет» нельзя:\n%q", out)
	}
	if a.reasonPastShown() {
		t.Error("во время хода блок мыслей не «прошлый»: перерисовка не нужна")
	}

	a.mu.Lock()
	a.running = false
	a.mu.Unlock()
}

// Один watcher на весь сеанс: нажатие обрабатывается и ход гасит его
// безопасно — повторный stopWatcher не паникует.
func TestWatcherHandlesCtrlOAndStops(t *testing.T) {
	a, _ := reasonApp(t)
	keys := make(chan byte, 4)
	a.stdin = &stdinReader{keys: keys}

	a.onReason("накоплено")
	// running = true, чтобы ctrlO пошёл по ветке хода: именно она включает
	// показ, который мы ниже и проверяем.
	a.mu.Lock()
	a.running = true
	a.mu.Unlock()
	w := a.startWatcher(a.ctrlO)

	keys <- ctrlO
	// Нажатие должно быть обработано: ждём включения показа.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		a.reasonMu.Lock()
		live := a.reasonLive
		a.reasonMu.Unlock()
		if live {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.reasonMu.Lock()
	live := a.reasonLive
	a.reasonMu.Unlock()
	if !live {
		t.Fatal("watchKeys не отреагировал на Ctrl+O")
	}

	w.stopWatcher()
	w.stopWatcher() // идемпотентно: defer в main зовёт второй раз
}

// Канал клавиш закрыт — watcher тихо выходит, не наследуя панику.
func TestWatcherStopsOnClosedKeys(t *testing.T) {
	a, _ := reasonApp(t)
	keys := make(chan byte)
	a.stdin = &stdinReader{keys: keys}

	w := a.startWatcher(a.ctrlO)
	close(keys)

	done := make(chan struct{})
	go func() {
		defer close(done)
		w.stopWatcher()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher не завершился после закрытия канала клавиш")
	}
}

// Флаг reasonPast живёт между нажатием и перерисовкой приглашения, а
// clearReasonPast возвращает REPL к обычной усадке.
func TestReasonPastFlagLifecycle(t *testing.T) {
	a, _ := reasonApp(t)
	if a.reasonPastShown() {
		t.Fatal("до первого Ctrl+O флаг перерисовки поднят быть не может")
	}
	a.onReason("мысли")
	a.finishReason()
	a.showLastReason()
	if !a.reasonPastShown() {
		t.Fatal("после показа мыслей флаг перерисовки должен стоять")
	}
	a.clearReasonPast()
	if a.reasonPastShown() {
		t.Error("после перерисовки приглашения флаг должен быть снят")
	}
}
