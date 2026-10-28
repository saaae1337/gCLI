package tui

// ---------- Рисунок из пикселей ----------
//
// Эмодзи не используются: их вид зависит от шрифта и системы. Маскот и
// фирменные знаки рисуются пикселями в полублоках (▀ ▄ █). Полублок есть
// в любом моноширинном шрифте, а цвет задаётся truecolor — картинка
// выглядит одинаково везде, где вообще работает этот интерфейс.

// pixmap — сетка символов-пикселей.
type pixmap struct {
	w, h int
	c    []byte
}

func newPixmap(w, h int) *pixmap {
	p := &pixmap{w: w, h: h, c: make([]byte, w*h)}
	for i := range p.c {
		p.c[i] = ' '
	}
	return p
}

func (p *pixmap) set(x, y int, ch byte) {
	if x < 0 || y < 0 || x >= p.w || y >= p.h {
		return
	}
	p.c[y*p.w+x] = ch
}

func (p *pixmap) get(x, y int) byte {
	if x < 0 || y < 0 || x >= p.w || y >= p.h {
		return ' '
	}
	return p.c[y*p.w+x]
}

func (p *pixmap) rect(x0, y0, x1, y1 int, ch byte) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			p.set(x, y, ch)
		}
	}
}

// ellipse — залить эллипс.
func (p *pixmap) ellipse(cx, cy, rx, ry int, ch byte) {
	for y := -ry; y <= ry; y++ {
		for x := -rx; x <= rx; x++ {
			// +0.5 — чтобы маленькие круги не выходили ромбами.
			if float64(x*x)/float64(rx*rx)+float64(y*y)/float64(ry*ry) <= 1.0 {
				p.set(cx+x, cy+y, ch)
			}
		}
	}
}

// triangle — залить треугольник с вершиной сверху.
func (p *pixmap) triangle(apexX, apexY, halfBase, height int, ch byte) {
	for i := 0; i <= height; i++ {
		half := halfBase * i / height
		for x := apexX - half; x <= apexX+half; x++ {
			p.set(x, apexY+i, ch)
		}
	}
}

// bloom — маленькая четырёхлучевая искра.
func (p *pixmap) bloom(cx, cy, r int, ch byte) {
	if r <= 0 {
		p.set(cx, cy, ch)
		return
	}
	for i := -r; i <= r; i++ {
		p.set(cx+i, cy, ch)
		p.set(cx, cy+i, ch)
	}
	p.set(cx, cy, ch)
}

// drawPixmap — нарисовать пиксельную картинку полублоками.
//
// Каждая клетка экрана — два пикселя по вертикали: верхний цвет идёт в
// цвет символа, нижний — в фон. Прозрачный пиксель — фон интерфейса.
func drawPixmap(s *Screen, x, y int, p *pixmap, pal map[byte]Color, bg Color) {
	for py := 0; py < p.h; py += 2 {
		for px := 0; px < p.w; px++ {
			top := p.get(px, py)
			var bot byte = ' '
			if py+1 < p.h {
				bot = p.get(px, py+1)
			}
			ct, okT := pal[top]
			cb, okB := pal[bot]
			switch {
			case !okT && !okB:
				continue
			case okT && okB:
				s.Set(x+px, y+py/2, '▀', Style{FG: ct, BG: cb})
			case okT:
				s.Set(x+px, y+py/2, '▀', Style{FG: ct, BG: bg})
			default:
				s.Set(x+px, y+py/2, '▄', Style{FG: cb, BG: bg})
			}
		}
	}
}

// MascotState — настроение Искры.
type MascotState int

const (
	MascotIdle MascotState = iota
	MascotBlink
	MascotHappy
	MascotThink
	MascotSleep
	MascotWork // занят: глаза в фокусе, искра крутится
	MascotDone // ход завершён: довольная морда и салют
)

// mascot — портрет Искры: угольный кот с искрой над головой.
//
// Сетка 22×22 пикселя: 22 клетки в ширину и 11 строк в высоту. Символы:
//
//	o — контур, f — шерсть, l — светлая шерсть, e — глаза,
//	k — зрачки и рот, n — нос, s — искра.
func mascot(state MascotState) (*pixmap, map[byte]Color) {
	p := newPixmap(24, 22)

	// Искра над головой: у каждого состояния своя.
	switch state {
	case MascotThink:
		p.bloom(12, 1, 1, 's')
		p.set(9, 1, 's')
		p.set(15, 1, 's')
	case MascotHappy, MascotDone:
		p.bloom(12, 1, 2, 's')
	case MascotWork:
		// Искра летает по дуге: кадр анимируется снаружи сдвигом.
		p.bloom(12, 1, 1, 's')
		p.set(8, 2, 's')
		p.set(16, 2, 's')
	default:
		p.bloom(12, 1, 1, 's')
	}

	// Уши: внешний контур и светлая внутренность. Вершины выше головы —
	// иначе уши сливаются с макушкой и портрет читается как лист.
	p.triangle(6, 2, 4, 5, 'o')
	p.triangle(18, 2, 4, 5, 'o')
	p.triangle(6, 4, 2, 3, 'l')
	p.triangle(18, 4, 2, 3, 'l')

	// Голова.
	p.ellipse(12, 13, 10, 6, 'o')
	p.ellipse(12, 13, 9, 5, 'f')

	// Мордочка.
	p.ellipse(12, 17, 5, 2, 'l')

	// Глаза.
	switch state {
	case MascotBlink, MascotSleep:
		p.rect(7, 12, 10, 12, 'o')
		p.rect(14, 12, 17, 12, 'o')
	case MascotHappy, MascotDone:
		// Довольные дуги: ^ ^
		p.set(7, 13, 'o')
		p.set(8, 12, 'o')
		p.set(9, 12, 'o')
		p.set(10, 13, 'o')
		p.set(14, 13, 'o')
		p.set(15, 12, 'o')
		p.set(16, 12, 'o')
		p.set(17, 13, 'o')
	case MascotWork:
		// Глаза в фокусе: чуть уже и ниже, зрачки к центру —
		// кот смотрит на задачу, а не в пространство.
		p.rect(8, 12, 10, 14, 'e')
		p.rect(14, 12, 16, 14, 'e')
		p.rect(9, 13, 10, 13, 'k')
		p.rect(14, 13, 15, 13, 'k')
	case MascotThink:
		// Взгляд вверх: зрачки у бровей.
		p.rect(7, 11, 10, 14, 'e')
		p.rect(14, 11, 17, 14, 'e')
		p.rect(8, 11, 9, 12, 'k')
		p.rect(15, 11, 16, 12, 'k')
	default:
		p.rect(7, 11, 10, 14, 'e')
		p.rect(14, 11, 17, 14, 'e')
		p.rect(8, 12, 9, 13, 'k')
		p.rect(15, 12, 16, 13, 'k')
	}

	// Нос и рот.
	p.rect(11, 16, 12, 16, 'n')
	p.set(11, 17, 'k')
	p.set(13, 17, 'k')
	p.set(12, 18, 'k')

	// Усы.
	p.set(4, 16, 'o')
	p.set(5, 16, 'o')
	p.set(6, 17, 'o')
	p.set(19, 16, 'o')
	p.set(18, 16, 'o')
	p.set(17, 17, 'o')

	pal := map[byte]Color{
		'o': Hex("#2B2118"),
		'f': Hex("#D08A3E"),
		'l': Hex("#EFC08A"),
		'e': Hex("#1A1410"),
		'k': Hex("#120E0A"),
		'n': Hex("#E48A8A"),
		's': Hex("#FFD37A"),
	}
	switch state {
	case MascotThink:
		pal['e'] = Hex("#7DC4E8")
	case MascotWork:
		// При работе искра горячее: состояние должно читаться
		// периферийным зрением, без чтения текста.
		pal['s'] = Hex("#FFB347")
	case MascotDone:
		pal['s'] = Hex("#9EE493")
	}
	return p, pal
}

// drawMascot — нарисовать маскота в позиции.
func drawMascot(s *Screen, x, y int, state MascotState, bg Color) {
	p, pal := mascot(state)
	drawPixmap(s, x, y, p, pal, bg)
}

// wordmark — стилизованное имя продукта с градиентом от охры к золоту.
func wordmark(t Theme, word string) []Seg {
	runes := []rune(word)
	out := make([]Seg, 0, len(runes))
	for i, r := range runes {
		col := t.Accent
		if len(runes) > 1 {
			col = Mix(t.Accent, t.Gold, float64(i)/float64(len(runes)-1))
		}
		out = append(out, Seg{Text: string(r), S: Style{FG: col, BG: t.Panel, Bold: true}})
	}
	return out
}

// spinnerFrames — кадры ожидания: искра вращается.
var spinnerFrames = []string{"✧", "✦", "✶", "✷", "✸", "✹", "✺", "✹", "✸", "✷", "✶", "✦"}

// spinner — текущий кадр.
func spinner(tick int) string {
	if tick < 0 {
		tick = 0
	}
	return spinnerFrames[tick%len(spinnerFrames)]
}

// ---------- Мини-Искра у поля ввода ----------
//
// Большой портрет живёт на заставке. В ленте коту выделяется узкая колонка
// слева от поля ввода: 5 клеток на 3 строки. Мало для деталей — достаточно
// для читаемого настроения: уши, глаза, искра. Состояния те же, что у
// большого, плюс анимация хвостом по краю.

// miniMascot — маленький портрет: сетка 10×6 пикселей (5×3 клетки).
//
// Символы те же, что у большого: o контур, f шерсть, e глаза, k зрачки,
// s искра, l светлая шерсть.
func miniMascot(state MascotState, phase int) (*pixmap, map[byte]Color) {
	p := newPixmap(10, 6)

	// Искра между ушей: мигает и перемещается — живой кот, а не логотип.
	switch state {
	case MascotWork, MascotThink:
		// Искра ходит по дуге: позиция зависит от фазы кадра.
		switch phase % 3 {
		case 0:
			p.set(4, 0, 's')
		case 1:
			p.set(5, 0, 's')
		default:
			p.set(5, 1, 's')
		}
	case MascotDone, MascotHappy:
		p.set(4, 0, 's')
		p.set(5, 0, 's')
	case MascotBlink:
		// Моргает — искра спокойная.
		p.set(4, 0, 's')
	default:
		if phase%2 == 0 {
			p.set(4, 0, 's')
		} else {
			p.set(5, 0, 's')
		}
	}

	// Уши.
	p.set(1, 0, 'o')
	p.set(1, 1, 'f')
	p.set(7, 0, 'o')
	p.set(7, 1, 'f')

	// Голова: контур по краям, шерсть внутри.
	p.rect(0, 2, 9, 5, 'o')
	p.rect(1, 3, 8, 5, 'f')
	p.set(1, 2, 'o')
	p.set(8, 2, 'o')
	// Хвост слева: качается фазой.
	if phase%2 == 0 {
		p.set(0, 5, 'o')
	} else {
		p.set(0, 4, 'o')
	}

	// Глаза и мордочка.
	switch state {
	case MascotBlink, MascotSleep:
		p.set(3, 3, 'o')
		p.set(6, 3, 'o')
	case MascotHappy, MascotDone:
		// ^ ^
		p.set(2, 3, 'o')
		p.set(3, 2, 'o')
		p.set(4, 3, 'o')
		p.set(5, 3, 'o')
		p.set(6, 2, 'o')
		p.set(7, 3, 'o')
	case MascotWork, MascotThink:
		p.rect(2, 3, 4, 4, 'e')
		p.rect(5, 3, 7, 4, 'e')
		p.set(3, 4, 'k')
		p.set(6, 4, 'k')
	default:
		p.rect(2, 3, 4, 4, 'e')
		p.rect(5, 3, 7, 4, 'e')
		p.set(3, 3, 'k')
		p.set(6, 3, 'k')
		p.set(4, 5, 'n')
	}
	pal := map[byte]Color{
		'o': Hex("#2B2118"),
		'f': Hex("#D08A3E"),
		'l': Hex("#EFC08A"),
		'e': Hex("#1A1410"),
		'k': Hex("#120E0A"),
		'n': Hex("#E48A8A"),
		's': Hex("#FFD37A"),
	}
	switch state {
	case MascotThink:
		pal['e'] = Hex("#7DC4E8")
	case MascotWork:
		pal['s'] = Hex("#FFB347")
	case MascotDone, MascotHappy:
		pal['s'] = Hex("#9EE493")
	}
	return p, pal
}

// drawMiniMascot — мини-кот: (x,y) — левый верхний угол области 5×3 клетки.
func drawMiniMascot(s *Screen, x, y int, state MascotState, phase int, bg Color) {
	p, pal := miniMascot(state, phase)
	drawPixmap(s, x, y, p, pal, bg)
}
