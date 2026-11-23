package ui

import (
	"math"
	"strings"
	"time"
)

// ---------- Перелив строки ожидания ----------
//
// Слово («думаю...», «работаю...») переливается цветом: волна бежит по
// кольцу длиной в слово, ядро кометы никогда не уезжает за края. Раньше
// эффект жил рядом с маскотом; после демонтажа маскота остался сам по
// себе — это украшение строки ожидания, а не мордочка.

// shimmerWord — раскрасить слово волной. t — секунды с запуска строки.
//
// На 16-цветном терминале палитра бедная, но перелив всё равно нужен: иначе
// слово стоит мёртвым. Там шкала идёт восемью ступенями от тусклого к добела,
// каждая отличается от соседней, и глаз видит движение даже там, где
// оттенков всего восемь.
func (u *UI) shimmerWord(word string, t float64) string {
	if word == "" {
		return ""
	}
	// Цвет выключен (-no-color, -json, NO_COLOR, пайп) - переливать нечем,
	// и любой escape-код здесь был бы мусором в выводе.
	if !u.opts.Color || u.quiet {
		return word
	}
	r := []rune(word)
	n := len(r)
	if n == 0 {
		return word
	}
	rt := rgbThemeFor("")

	// Фронт идёт по кольцу длиной в слово: ядро кометы никогда не покидает
	// буквы, хвост тянется за ним по кругу, а в полкруга от фронта идёт
	// вторая, тусклая комета - чтобы перелив читался движением даже в
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
		// Обычный |front-x| давал бы на стыке слова скачок цвета - там
		// буквы «...» и «t» оказывались бы по разные стороны фронта.
		d := math.Abs(front - x)
		if d > ring/2 {
			d = ring - d
		}
		k := gauss(d / shimmerSigma)
		// Хвост кометы позади фронта: идём от буквы вперёд по кольцу.
		behind := math.Mod(front-x, ring)
		k += shimmerTailK * gauss((behind-shimmerTail)/(shimmerSigma*2.4))
		// Вторая комета - вдвое слабее и вдвое шире первой.
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
	// чтобы фронт волны читался.
	//
	// Ключевой момент: нижняя ступень - тусклый уголь, а НЕ серый.
	// Раньше порог k<=0.02 отдавал камень серым, и на 16-цветном терминале
	// половина шкалы (idx 0..2) попадала в ту же серую «базу»: фронт волны
	// был виден только в верхней трети цвета. Теперь каждый уровень шкалы
	// отличается от соседнего, и перелив читается целиком.
	basic := [][3]int{
		{72, 78, 110},   // тусклый камень
		{90, 98, 140},   //
		{110, 120, 170}, //
		{122, 140, 200}, // синий
		{140, 160, 225}, //
		{160, 185, 240}, //
		{190, 210, 248}, //
		{225, 235, 252}, // добела
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
			c := blendRGB(rt.Muted, rt.Accent, k)
			if k > 0.82 {
				c = blendRGB(rt.Accent, [3]int{230, 238, 252}, (k-0.82)/0.18)
			}
			code = codeFor(c, u.grade)
		}
		b.WriteString(code + string(ch))
	}
	b.WriteString(cReset)
	return b.String()
}

// Параметры перелива. Волна идёт по кольцу длиной в слово со скоростью
// shimmerSpeed колонок в секунду, ядро - шириной в shimmerSigma колонок,
// хвост тянется на shimmerTail колонок позади фронта. Вторая комета идёт
// в полкруга от первой и слабее неё в shimmerSecondK раз.
const (
	shimmerSpeed   = 9.0
	shimmerSigma   = 1.15
	shimmerTail    = 2.6
	shimmerTailK   = 0.45
	shimmerSecondK = 0.30
)

// gauss - гауссова кривая: 1 в нуле, быстро гаснет по краям.
func gauss(x float64) float64 { return math.Exp(-x * x) }

// shimmerElapsed - округлённые секунды строки ожидания.
func shimmerElapsed(d time.Duration) string {
	return d.Truncate(time.Second).String()
}
