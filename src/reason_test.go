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

// turnWatcher реагирует на Ctrl+O и молча уходит по stop.
func TestTurnWatcherHandlesCtrlO(t *testing.T) {
	a, _ := reasonApp(t)
	keys := make(chan byte, 4)
	a.stdin = &stdinReader{keys: keys}

	a.onReason("накоплено")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.turnWatcher(stop)
	}()

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

	close(stop)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("turnWatcher не завершился по stop")
	}
}
