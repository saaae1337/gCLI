package tui

import "testing"

// TestParseCSIModifiers — Enter и Tab с модификаторами по kitty-протоколу
// и modifyOtherKeys: Shift+Enter отправляет, Alt+Enter остаётся запасным,
// Shift+Tab — обратный таб, обычные байты не задеты.
func TestParseCSIModifiers(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Event
		n    int
	}{
		{"kitty: shift+enter", "\x1b[13;2u", Event{Key: KeyEnter, Shift: true}, 7},
		{"kitty: ctrl+enter", "\x1b[13;5u", Event{Key: KeyEnter, Ctrl: true}, 7},
		{"kitty: alt+enter", "\x1b[13;3u", Event{Key: KeyEnter, Alt: true}, 7},
		{"kitty: enter без моды", "\x1b[13u", Event{Key: KeyEnter}, 5},
		{"kitty: shift+tab", "\x1b[9;2u", Event{Key: KeyBacktab, Shift: true}, 6},
		{"kitty: tab", "\x1b[9u", Event{Key: KeyTab}, 4},
		{"mok: shift+enter", "\x1b[27;2;13~", Event{Key: KeyEnter, Shift: true}, 10},
		{"mok: alt+enter", "\x1b[27;3;13~", Event{Key: KeyEnter, Alt: true}, 10},
		{"kitty: ответ флагов", "\x1b[?1u", Event{}, 5},
		{"legacy: alt+enter", "\x1b\r", Event{Key: KeyEnter, Alt: true}, 2},
		{"legacy: enter", "\r", Event{Key: KeyEnter}, 1},
		{"legacy: ctrl+j", "\n", Event{Key: KeyRune, R: 'j', Ctrl: true}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, n, need := parseKey([]byte(tc.in), false)
			if need {
				t.Fatalf("need=true, хотим разобранное событие")
			}
			if ev != tc.want {
				t.Fatalf("ev=%+v, хочу %+v", ev, tc.want)
			}
			if n != tc.n {
				t.Fatalf("n=%d, хочу %d", n, tc.n)
			}
		})
	}
}
