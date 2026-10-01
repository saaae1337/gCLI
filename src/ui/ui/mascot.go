package ui

import (
	"math"
	"strings"
	"time"
)

// ---------- Маскот «ИСКРА» — кот gcli ----------
//
// Искра — кот-помощник, который всегда на посту. Она встречает в баннере,
// сидит над строкой состояния, пока пользователь печатает, думает и
// работает вместе с ИИ, мурчит от результата и фыркает на ошибках.
//
// Кот нарисован по референсу проекта — три строки:
//
//     /\_/\
//    ( ^.^ )
//     > ^ <
//
// Оформление подчиняется теме «УГОЛЬ»: контур — охра, мордочка — тёплый
// белый, хвост-суффикс (zZ, ?) — светлая охра.
//
// Правила арта:
//
//  1. Каждый кадр — ровно три строки: уши, мордочка, лапы. Эмоция живёт
//     в трёх символах между скобками, поэтому кот анимируется без сдвигов:
//     правая колонка баннера и строка состояния под ним остаются на месте.
//
//  2. У каждого состояния есть ASCII-версия: в режиме -ascii кот продолжает
//     жить — просто грубее нарисован (ω → w, ° → o, □ → O, △ → o).
//
//  3. Все состояния одного набора — одной ширины: строка мордочки добивается
//     пробелами до catWidth, суффиксы (zZ, ?) растут вправо от неё.

// MascotState — состояние маскота.
type MascotState int

const (
	MascotIdle      MascotState = iota // ждёт команду            ( ^.^ )
	MascotThink                        // модель рассуждает       ( -.- )
	MascotWork                         // идут инструменты        ( >.< )
	MascotHappy                        // задача выполнена        ( ^ω^ )
	MascotOops                         // что-то сломалось        ( x_x )
	MascotSleep                        // сессия закрыта / сон    ( -.- ) zZ
	MascotListen                       // слушает пользователя    ( °.° )
	MascotSurprised                    // неожиданный результат   ( °□° )
	MascotBored                        // долго нет команд        ( -.- )
	MascotEat                          // большой объём данных    ( ~.~ )
	MascotBlink                        // моргает                 ( -.- )
	MascotWink                         // подмигивает             ( ^.~ )
	MascotContent                      // доволен                 ( ^_^ )
	MascotShy                          // смущён                  ( ^//^ )
	MascotCry                          // плачет                  ( T_T )
	MascotAngry                        // сердится                ( Ò_Ó )
	MascotPensive                      // задумчив                ( -.- )?
	MascotScared                       // испуган                 ( °△° )
	MascotPurr                         // мурлычет                ( ^~^ )
)

// mascotLast — последнее состояние (для перебора в тестах и демо).
const mascotLast = MascotPurr

// Geometry кота: три строки, тело мордочки — catWidth колонок.
const (
	catHeight = 3
	catWidth  = 8
)

// mascotFrame — один кадр: лицо (3 символа) и хвост-суффикс.
type mascotFrame struct {
	face   string
	suffix string
}

// catLines — собрать три строки кота из кадра. Строки добиваются до
// catWidth, суффикс растёт вправо от тела.
func catLines(f mascotFrame) []string {
	l2 := " ( " + f.face + " )"
	if p := catWidth - runeLen(l2); p > 0 {
		l2 += strings.Repeat(" ", p)
	}
	l2 += f.suffix
	return []string{
		padTo("  /\\_/\\", catWidth),
		l2,
		padTo("  > ^ <", catWidth),
	}
}

// ---------- Лица по референсу ----------

// mascotFaces — юникод-лица состояний. Каноническое лицо кадра 0.
func mascotFaces(s MascotState) []mascotFrame {
	switch s {
	case MascotThink:
		return []mascotFrame{{face: "-.-"}}
	case MascotWork:
		return []mascotFrame{{face: ">.<"}}
	case MascotHappy:
		return []mascotFrame{{face: "^ω^"}, {face: "^.^"}}
	case MascotOops:
		return []mascotFrame{{face: "x_x"}}
	case MascotSleep:
		return []mascotFrame{{"-.-", " zZ"}}
	case MascotListen:
		return []mascotFrame{{face: "°.°"}}
	case MascotSurprised:
		return []mascotFrame{{face: "°□°"}}
	case MascotBored:
		return []mascotFrame{{face: "-.-"}}
	case MascotEat:
		return []mascotFrame{{face: "~.~"}}
	case MascotBlink:
		return []mascotFrame{{face: "-.-"}}
	case MascotWink:
		return []mascotFrame{{face: "^.~"}, {face: "^.^"}, {face: "^.~"}}
	case MascotContent:
		return []mascotFrame{{face: "^_^"}}
	case MascotShy:
		return []mascotFrame{{face: "^//^"}}
	case MascotCry:
		return []mascotFrame{{face: "T_T"}}
	case MascotAngry:
		return []mascotFrame{{face: "Ò_Ó"}}
	case MascotPensive:
		return []mascotFrame{{"-.-", "?"}}
	case MascotScared:
		return []mascotFrame{{face: "°△°"}}
	case MascotPurr:
		return []mascotFrame{{face: "^~^"}, {face: "^.^"}}
	default:
		// Ждёт команду: изредка моргает.
		return []mascotFrame{{face: "^.^"}, {face: "^.^"}, {face: "-.-"}, {face: "^.^"}}
	}
}

// mascotASCIIFaces — те же состояния для терминалов без юникода.
func mascotASCIIFaces(s MascotState) []mascotFrame {
	switch s {
	case MascotThink, MascotBlink, MascotBored, MascotPensive:
		f := "-.-"
		suffix := ""
		if s == MascotPensive {
			suffix = "?"
		}
		return []mascotFrame{{f, suffix}}
	case MascotWork:
		return []mascotFrame{{face: ">.<"}}
	case MascotHappy:
		return []mascotFrame{{face: "^w^"}, {face: "^.^"}}
	case MascotOops:
		return []mascotFrame{{face: "x_x"}}
	case MascotSleep:
		return []mascotFrame{{"-.-", " zZ"}}
	case MascotListen:
		return []mascotFrame{{face: "o.o"}}
	case MascotSurprised:
		return []mascotFrame{{face: "O.O"}}
	case MascotEat:
		return []mascotFrame{{face: "~.~"}}
	case MascotWink:
		return []mascotFrame{{face: "^.~"}, {face: "^.^"}, {face: "^.~"}}
	case MascotContent:
		return []mascotFrame{{face: "^_^"}}
	case MascotShy:
		return []mascotFrame{{face: "^//^"}}
	case MascotCry:
		return []mascotFrame{{face: "T_T"}}
	case MascotAngry:
		return []mascotFrame{{face: ">_<"}}
	case MascotScared:
		return []mascotFrame{{face: "o.O"}}
	case MascotPurr:
		return []mascotFrame{{face: "^~^"}, {face: "^.^"}}
	default:
		return []mascotFrame{{face: "^.^"}, {face: "^.^"}, {face: "-.-"}, {face: "^.^"}}
	}
}

// MascotFrames — кадры состояния под текущий терминал.
func (u *UI) MascotFrames(s MascotState) []mascotFrame {
	if u.opts.Unicode {
		return mascotFaces(s)
	}
	return mascotASCIIFaces(s)
}

// mascotFrameOf — кадр по индексу (по кругу).
func (u *UI) mascotFrameOf(s MascotState, frame int) mascotFrame {
	frames := u.MascotFrames(s)
	if len(frames) == 0 {
		return mascotFrame{face: "^.^"}
	}
	return frames[frame%len(frames)]
}

// ---------- Раскраска ----------

// mascotPaint — раскрасить кадр под тему «УГОЛЬ»: контур — охра,
// мордочка — тёплый белый, суффикс (zZ, ?) — светлая охра.
func (u *UI) mascotPaint(f mascotFrame) string {
	lines := catLines(f)
	var b strings.Builder
	for i, line := range lines {
		b.WriteString(u.mascotPaintLine(i, line))
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// mascotPaintLine — раскрасить одну строку кадра.
func (u *UI) mascotPaintLine(lineIdx int, line string) string {
	switch lineIdx {
	case 1:
		return u.mascotPaintFace(line)
	default:
		// Уши и лапы — цельный контур акцентом.
		return u.c(u.pal.Accent, line)
	}
}

// mascotPaintFace — строка мордочки: скобки акцентом, лицо — цветом
// заголовков, суффикс (zZ, ?) — светлой охрой.
func (u *UI) mascotPaintFace(line string) string {
	r := []rune(line)
	open, close := -1, -1
	for i, ch := range r {
		if ch == '(' {
			open = i
			break
		}
	}
	for i := len(r) - 1; i >= 0; i-- {
		if r[i] == ')' {
			close = i
			break
		}
	}
	if open < 0 || close < open {
		return u.c(u.pal.Accent, line)
	}
	var b strings.Builder
	b.WriteString(u.c(u.pal.Accent, string(r[:open+1])))
	for i := open + 1; i < close; i++ {
		if r[i] == ' ' {
			b.WriteString(" ")
			continue
		}
		b.WriteString(u.c(u.pal.Head, string(r[i])))
	}
	// Закрывающая скобка — акцентом, суффикс (zZ, ?) — светлой охрой.
	// Суффикс красится на своём месте: он уже часть строки, и второй раз
	// дописывать его нельзя.
	b.WriteString(u.c(u.pal.Accent, string(r[close:close+1])))
	if tail := string(r[close+1:]); strings.TrimSpace(tail) != "" {
		b.WriteString(u.c(u.pal.Warn, tail))
	}
	return b.String()
}

// MascotRender — готовый к печати кадр (с цветом).
func (u *UI) MascotRender(s MascotState, frame int) string {
	return u.mascotPaint(u.mascotFrameOf(s, frame))
}

// mascotInline — мордочка одной строкой для спиннера и реплик: «( -.- )».
// Лицо живёт без пробелов внутри скобок — короче и ближе к кадру кота.
func (u *UI) mascotInline(s MascotState, frame int) string {
	f := u.mascotFrameOf(s, frame)
	return u.c(u.pal.Accent, "("+f.face+")")
}

// ---------- Анимации с маскотом ----------

// mascotAnimStep — пауза между кадрами маскота. Медленнее «раскрытия»
// строк: кот живёт, а не мелькает.
const mascotAnimStep = 260 * time.Millisecond

// canMascot — можно ли показывать живого маскота.
//
// Маскоту не нужен цвет, чтобы существовать (он нарисован символами),
// но нужны анимации и настоящий вывод. В quiet-режиме (-json, пайпы)
// маскота нет.
func (u *UI) canMascot() bool {
	return u.opts.Animations && !u.quiet
}

// MascotSpeak — маскот говорит: кадр состояния + реплика в «облачке»
// из тире. Без рамок — в духе плоской вёрстки темы.
func (u *UI) MascotSpeak(s MascotState, text string) {
	u.Println(u.MascotRender(s, 0))
	if text != "" {
		indent := strings.Repeat(" ", catWidth+3)
		for _, l := range wrap(text, u.Width()-catWidth-6) {
			u.Println(indent + u.Gray("╭ "+l))
		}
		// Хвост облачка указывает на маскота.
		u.Println(indent + u.Gray("╰──────────"))
	}
}

// MascotSayLine — короткая реплика маскота одной строкой (без облачка):
// «  ( -.- )  думает».
func (u *UI) MascotSayLine(s MascotState, text string) {
	u.Println("  " + u.mascotInline(s, 0) + "  " + text)
}

// MascotDance — проиграть анимацию состояния на месте (demo / приветствие):
// кадры сменяют друг друга, в конце остаётся последний кадр.
func (u *UI) MascotDance(s MascotState, cycles int) {
	frames := u.MascotFrames(s)
	if len(frames) == 0 {
		return
	}
	if !u.canMascot() || len(frames) == 1 {
		u.Println(u.MascotRender(s, 0))
		return
	}
	for range catLines(frames[0]) {
		u.RawWrite("\n")
	}
	for c := 0; c < cycles; c++ {
		for i := range frames {
			lines := catLines(frames[i])
			u.RawWrite("\r\033[" + itoa(catHeight-1) + "A\r")
			for j, line := range lines {
				if j > 0 {
					u.RawWrite("\n")
				}
				u.RawWrite("\033[K" + u.mascotPaintLine(j, line))
			}
			time.Sleep(mascotAnimStep)
		}
	}
	u.RawWrite("\r\033[" + itoa(catHeight-1) + "A\r")
	last := catLines(frames[len(frames)-1])
	for j, line := range last {
		if j > 0 {
			u.RawWrite("\n")
		}
		u.RawWrite("\033[K" + u.mascotPaintLine(j, line))
	}
	u.RawWrite("\n")
}

// ---------- Кот над строкой состояния ----------

// Пороги анимации простоя: сначала кот просто живёт (моргает), потом
// скучает, а после долгой паузы засыпает.
const (
	idleBoreAfter  = 8 * time.Second  // начинает скучать
	idleSleepAfter = 25 * time.Second // засыпает по-настоящему
)

// MascotPerch — усадить кота над строкой состояния. Вызывается перед
// каждым приглашением: кот всегда на посту, прямо над статусом
// «модель · состояние · режим». Порядок печати важен: кот занимает
// ровно catHeight строк, и фоновая перерисовка знает, где он сидит.
func (u *UI) MascotPerch() {
	if !u.mascot || u.quiet {
		return
	}
	u.Println(u.MascotRender(MascotIdle, 0))
}

// mascotRedrawTop — сколько строк подниматься от строки ввода до макушки
// кота: сам кот плюс строка состояния между котом и приглашением.
const mascotRedrawTop = catHeight + 1

// idleMood — что делает кот спустя d простоя: моргает, скучает или спит.
// Чистая функция времени — чтобы её можно было проверить тестом.
func idleMood(d time.Duration) (MascotState, int) {
	sec := int(d.Seconds())
	switch {
	case sec < int(idleBoreAfter.Seconds()):
		// Бодрствует: покой, изредка морг.
		if sec%6 == 3 {
			return MascotBlink, 0
		}
		return MascotIdle, (sec / 6) % 2
	case sec < int(idleSleepAfter.Seconds()):
		// Скучает: долго нет команд.
		return MascotBored, 0
	default:
		// Спит: один кадр по референсу, без мерцания zZ/Zz.
		return MascotSleep, 0
	}
}

// mascotRedraw — перерисовать кота над строкой состояния.
//
// Вызывается только из idleLoop под мьютексом и только при поднятой
// привязке (mascotAnchored): курсор стоит на строке приглашения, над ним
// ровно catHeight строк кота и строка статуса. Курсор сохраняется (ESC7)
// и восстанавливается (ESC8): строка состояния, приглашение и то, что
// пользователь уже набрал, не затрагиваются. Запись идёт напрямую в
// u.out — RawWrite здесь нельзя, он сбросил бы привязку, которую мы
// как раз проверяем.
func (u *UI) mascotRedraw(s MascotState, frame int) {
	if !u.mascot || u.quiet {
		return
	}
	u.mascotRedrawLines(catLines(u.mascotFrameOf(s, frame)))
}

// mascotRedrawLines — записать готовые строки кадра с сохранением курсора.
// Вызывается только под мьютексом при поднятой привязке.
func (u *UI) mascotRedrawLines(lines []string) {
	var b strings.Builder
	b.WriteString("\0337")
	b.WriteString("\033[" + itoa(mascotRedrawTop) + "A")
	for j, line := range lines {
		if j > 0 {
			b.WriteString("\n")
		}
		b.WriteString("\r\033[K" + u.mascotPaintLine(j, line))
	}
	b.WriteString("\0338")
	_, _ = u.out.Write([]byte(b.String()))
}

// MascotIdleStart — усадить кота на пост и запустить его фоновую жизнь:
// моргает, скучает и засыпает, пока пользователь не ответил.
//
// Вызывается сразу после приглашения к вводу: с этой точки курсор стоит
// на строке приглашения, и перерисовка знает, где сидит кот. Простой
// отсчитывается заново от каждой усадки — кот не засыпает сразу после
// команды только потому, что старый таймер не остановлен.
func (u *UI) MascotIdleStart() {
	if !u.canMascot() || !u.mascot {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.mascotAnchored = true
	u.idleSince = time.Now()
	if u.idleOn {
		return
	}
	u.idleOn = true
	u.idleStop = make(chan struct{})
	u.idleDone = make(chan struct{})
	go u.idleLoop(u.idleStop, u.idleDone)
}

// idleLoop — цикл фоновой анимации простоя.
//
// Каждый тик атомарно проверяет состояние под мьютексом: цикл гаснет,
// если его остановили, и молчит, если привязка сброшена любой другой
// печатью. Проверка и запись — один критический участок, поэтому чужой
// вывод физически не может вклиниться в середину кадра.
//
// Кадр пишется только когда он реально изменился: кот моргает раз в 6
// секунд, а не перепечатывается каждые 0,9 с. Меньше записей в терминал —
// меньше мерцания на Windows и меньше шансов встретиться с чужим выводом
// в уязвимое окно после Enter.
func (u *UI) idleLoop(stop, done chan struct{}) {
	defer close(done)
	t := time.NewTicker(900 * time.Millisecond)
	defer t.Stop()
	var last string
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			u.mu.Lock()
			if !u.idleOn {
				// Страховка: если стоп не дошёл, закрываемся сами.
				u.mu.Unlock()
				return
			}
			if !u.mascotAnchored {
				u.mu.Unlock()
				continue
			}
			state, frame := idleMood(time.Since(u.idleSince))
			lines := catLines(u.mascotFrameOf(state, frame))
			key := strings.Join(lines, "\n")
			if key == last {
				// Кадр не менялся — терминал не трогаем.
				u.mu.Unlock()
				continue
			}
			last = key
			u.mascotRedrawLines(lines)
			u.mu.Unlock()
		}
	}
}

// MascotIdleStop — остановить фоновую жизнь кота. Ждёт завершения
// кадра, чтобы вывод хода не столкнулся с перерисовкой. Привязка к
// строке приглашения сбрасывается: после Enter курсор уехал вниз, и
// старая точка перерисовки стала ложной.
func (u *UI) MascotIdleStop() {
	u.mu.Lock()
	u.mascotAnchored = false
	if !u.idleOn {
		u.mu.Unlock()
		return
	}
	u.idleOn = false
	stop, done := u.idleStop, u.idleDone
	u.mu.Unlock()
	close(stop)
	select {
	case <-done:
	case <-time.After(time.Second):
		// Не зависаем: кадр перерисовки занимает миллисекунды, секунда —
		// с запасом. Если что-то пошло не так, просто идём дальше.
	}
}

// ---------- Перелив «thinking...» ----------

// shimmerWord — слово с цветной волной, которая бежит по нему по кругу:
// «thinking...» переливается до самого конца ожидания, как неоновая
// вывеска. Волна — «комета»: яркое ядро и тёплый хвост позади.
//
// Цвет строится на лету из палитры «УГЛЯ»: база — тлеющий уголь, ядро —
// светлая охра, пик добеляется. Фронт ходит по кольцу длиной в слово,
// поэтому ядро никогда не уезжает за край и строка не гаснет в статичный
// камень.
//
// На 16-цветном терминале палитра бедная, но перелив всё равно нужен: иначе
// слово стоит мёртвым. Там шкала идёт восемью ступенями от угля к добела,
// каждая отличается от соседней, и глаз видит движение даже там, где
// оттенков всего восемь.
func (u *UI) shimmerWord(word string, t float64) string {
	if word == "" {
		return ""
	}
	// Цвет выключен (-no-color, -json, NO_COLOR, пайп) — переливать нечем,
	// и любой escape-код здесь был бы мусором в выводе.
	if !u.opts.Color || u.quiet {
		return word
	}
	// Позиция берётся по модулю ДЛИНЫ СЛОВА, а не по отдельному пробегу с
	// разворотом. Прежний ход «вправо до конца, потом обратно» держал фронт
	// половину времени в холостом пробеге за краями слова, и перелив выглядел
	// как одно яркое пятно, ползущее мимо серого кирпича: глаз цеплялся за
	// пустые участки и через пару секунд переставал читать движение. По кольцу
	// фронт всегда на слове, поэтому строка жива все время ожидания.
	r := []rune(word)
	n := len(r)
	if n == 0 {
		return word
	}
	rt := rgbThemeFor("")

	// Фронт идёт по кольцу длиной в слово: ядро кометы никогда не покидает
	// буквы, хвост тянется за ним по кругу, а в полкруга от фронта идёт
	// вторая, тусклая комета — чтобы перелив читался движением даже в
	// паузах между «пробегами» ядра.
	ring := float64(n)
	front := math.Mod(math.Mod(t*shimmerSpeed, ring)+ring, ring)
	second := math.Mod(front-ring/2, ring)

	// Яркость символа = расстояние по кольцу до фронтов волны: ядро и
	// тёплый хвост позади.
	levels := make([]float64, n)
	for i := range r {
		if r[i] == ' ' {
			levels[i] = -1
			continue
		}
		x := float64(i)
		// Расстояние вдоль кольца: кратчайшая дуга между фронтом и буквой.
		// Обычный |front-x| давал бы на стыке слова скачок цвета — там
		// буквы «...» и «t» оказывались бы по разные стороны фронта.
		d := math.Abs(front - x)
		if d > ring/2 {
			d = ring - d
		}
		k := gauss(d / shimmerSigma)
		// Хвост кометы позади фронта: идём от буквы вперёд по кольцу.
		behind := math.Mod(front-x, ring)
		k += shimmerTailK * gauss((behind-shimmerTail)/(shimmerSigma*2.4))
		// Вторая комета — вдвое слабее и вдвое шире первой.
		d2 := math.Abs(second - x)
		if d2 > ring/2 {
			d2 = ring - d2
		}
		k += shimmerSecondK * gauss(d2/(shimmerSigma*2.0))
		if k > 1 {
			k = 1
		}
		levels[i] = k
	}

	// 16 цветов: настоящего градиента нет, но восемь ступеней достаточно,
	// чтобы фронт волны читался. Палитра упорядочена от тусклого к яркому.
	//
	// Ключевой момент: нижняя ступень — тёмно-красный уголь, а НЕ серый.
	// Раньше порог k<=0.02 отдавал камень серым, и на 16-цветном терминале
	// половина шкалы (idx 0..2) попадала в ту же серую «базу»: фронт волны
	// был виден только в верхней трети цвета. Теперь каждый уровень шкалы
	// отличается от соседнего, и перелив читается целиком.
	basic := [][3]int{
		{90, 24, 24},   // тлеющий уголь
		{140, 32, 32},  //
		{180, 45, 45},  //
		{205, 78, 49},  // охра
		{229, 130, 60}, //
		{245, 190, 110},
		{255, 235, 190},
		{255, 251, 240}, // добела
	}

	var b strings.Builder
	for i, ch := range r {
		if ch == ' ' {
			b.WriteRune(' ')
			continue
		}
		k := levels[i]
		var code string
		if k <= 0.02 {
			if u.grade < color256 {
				code = cGray
			} else {
				code = u.pal.Muted
			}
		} else if u.grade < color256 {
			idx := int(k * float64(len(basic)-1))
			code = nearestBasic(basic[idx])
		} else {
			c := blendRGB(rt.Muted, rt.Warn, k)
			if k > 0.82 {
				c = blendRGB(rt.Warn, [3]int{255, 251, 240}, (k-0.82)/0.18)
			}
			code = codeFor(c, u.grade)
		}
		b.WriteString(code + string(ch))
	}
	b.WriteString(cReset)
	return b.String()
}

// Параметры перелива. Волна идёт по кольцу длиной в слово со скоростью
// shimmerSpeed колонок в секунду, ядро — шириной в shimmerSigma колонок,
// хвост тянется на shimmerTail колонок позади фронта. Вторая комета идёт
// в полкруга от первой и слабее неё в shimmerSecondK раз.
const (
	shimmerSpeed   = 9.0
	shimmerSigma   = 1.15
	shimmerTail    = 2.6
	shimmerTailK   = 0.45
	shimmerSecondK = 0.30
)

// shimmerPeriod — период полного оборота фронта по слову в секундах.
// Чистая функция длины слова: тест проверяет цикличность по ней, а не
// дублируя формулу из shimmerWord.
func shimmerPeriod(word string) float64 {
	n := runeLen(word)
	if n < 1 {
		return 0
	}
	return float64(n) / shimmerSpeed
}

// gauss — гауссова кривая: 1 в нуле, быстро гаснет по краям.
func gauss(x float64) float64 { return math.Exp(-x * x) }

// shimmerElapsed — округлённые секунды строки ожидания.
func shimmerElapsed(d time.Duration) string {
	return d.Truncate(time.Second).String()
}
