package ui

import (
	"fmt"
	"strings"

	"gcli/core"
)

// DiffOp — одна операция диффа.
type DiffOp struct {
	Kind byte // ' ' контекст, '+' добавлено, '-' удалено
	Old  int  // номер строки в старом файле (0 = нет)
	New  int  // номер строки в новом файле (0 = нет)
	Text string
}

// DiffOpts — параметры показа диффа.
type DiffOpts struct {
	Path      string
	MaxLines  int
	Context   int
	NoStats   bool
	HunkNames bool
}

// Diff — построить операции диффа (алгоритм Майерса на LCS).
// Возвращает операции с номерами строк обоих файлов.
func Diff(oldS, newS string) []DiffOp {
	return DiffLines(core.SplitLines(oldS), core.SplitLines(newS))
}

// DiffLines — дифф по двум наборам строк.
func DiffLines(a, b []string) []DiffOp {
	n, m := len(a), len(b)

	// Очень большие файлы — линейный diff по префиксу/суффиксу.
	if n*m > 4_000_000 {
		return naiveDiff(a, b)
	}

	// Нумерация строк: old 1..n, new 1..m.
	// LCS-матрица.
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var ops []DiffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, DiffOp{' ', i + 1, j + 1, a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, DiffOp{'-', i + 1, 0, a[i]})
			i++
		default:
			ops = append(ops, DiffOp{'+', 0, j + 1, b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, DiffOp{'-', i + 1, 0, a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, DiffOp{'+', 0, j + 1, b[j]})
	}
	return ops
}

// naiveDiff — запасной линейный diff для огромных файлов: сравниваем
// общий префикс и общий суффикс, середину считаем полной заменой.
func naiveDiff(a, b []string) []DiffOp {
	var ops []DiffOp
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		ops = append(ops, DiffOp{' ', i + 1, i + 1, a[i]})
		i++
	}
	// Суффикс собираем отдельно и ставим после замены.
	var suffix []DiffOp
	sa, sb := len(a), len(b)
	for sa > i && sb > i && a[sa-1] == b[sb-1] {
		sa--
		sb--
		suffix = append([]DiffOp{{' ', sa + 1, sb + 1, a[sa]}}, suffix...)
	}
	for k := i; k < sa; k++ {
		ops = append(ops, DiffOp{'-', k + 1, 0, a[k]})
	}
	for k := i; k < sb; k++ {
		ops = append(ops, DiffOp{'+', 0, k + 1, b[k]})
	}
	return append(ops, suffix...)
}

// PrintDiff — напечатать дифф с номерами строк и контекстом.
func (u *UI) PrintDiff(ops []DiffOp, o DiffOpts) {
	ctx := o.Context
	if ctx <= 0 {
		ctx = 2
	}
	maxLines := o.MaxLines
	if maxLines <= 0 {
		maxLines = 100
	}

	added, removed := 0, 0
	isChange := make([]bool, len(ops))
	for i, op := range ops {
		switch op.Kind {
		case '+':
			added++
			isChange[i] = true
		case '-':
			removed++
			isChange[i] = true
		}
	}

	// Шапка.
	u.SpinnerStop()
	u.Println("")
	//  ◆ путь
	//    +3 · −1
	//
	// Маркер события вместо «✎»: дифф — это не отдельный значок,
	// а такой же шаг хода, как и вызов инструмента. Поэтому он встаёт
	// в один ряд с остальными маркерами, а не выпадает из потока.
	u.Println("  " + u.emberMark(u.pal.Accent) + " " + u.Head(core.OneLine(o.Path)))
	if !o.NoStats {
		u.Println("  " + u.emberTail() + " " + u.c(u.pal.Muted, strings.Join([]string{
			u.c(u.pal.OK, "+"+core.Kfmt(added)),
			u.c(u.pal.Err, "−"+core.Kfmt(removed)),
		}, " "+u.g.Dot+" ")))
	}

	if added == 0 && removed == 0 {
		u.Println("    " + u.Gray("(без изменений)"))
		return
	}

	// Какие операции показывать.
	show := make([]bool, len(ops))
	for i := range ops {
		if !isChange[i] {
			continue
		}
		for j := maxInt(0, i-ctx); j <= core.Min(len(ops)-1, i+ctx); j++ {
			show[j] = true
		}
	}
	// Склейка через ≤ 3 пропущенные строки.
	last := -1
	for i := range ops {
		if !show[i] {
			continue
		}
		if last >= 0 && i-last <= ctx+3 {
			for j := last + 1; j < i; j++ {
				show[j] = true
			}
		}
		last = i
	}

	shown, hidden := 0, 0
	prevShown := false
	for i := range ops {
		if !show[i] {
			prevShown = false
			continue
		}
		if !prevShown && i > 0 {
			u.printlnDiffGap(6)
		}
		if shown >= maxLines {
			hidden++
			continue
		}
		u.printDiffOp(ops[i])
		shown++
		prevShown = true
	}
	if hidden > 0 {
		u.printlnDiffGap(6)
		u.Println(strings.Repeat(" ", 6) + u.Gray(fmt.Sprintf("%s ещё %d строк диффа скрыто", u.g.Ellipsis, hidden)))
	}
}

// printlnDiffGap — разделитель пропущенных строк диффа.
func (u *UI) printlnDiffGap(indent int) {
	_ = indent
	// Пропуск обозначаем многоточием на зубце: он не спорит с плюсами
	// и минусами строк диффа, как это делает пунктир.
	u.Println("  " + u.emberTail() + " " + u.c(u.pal.Muted, u.g.Ellipsis))
}

// printDiffOp — одна строка диффа с номерами строк с обеих сторон.
func (u *UI) printDiffOp(op DiffOp) {
	var body string
	switch op.Kind {
	case '+':
		body = u.c(u.pal.OK, "+ ") + u.c(u.pal.OK, op.Text)
	case '-':
		body = u.c(u.pal.Err, "− ") + u.c(u.pal.Err, op.Text)
	default:
		body = u.c(u.pal.Muted, "  ") + u.c(u.pal.Muted, op.Text)
	}

	// Номера строк скрыты: важны сами изменения, а колонки цифр шумят.
	// Строка встаёт вровень с «зубцом» результата, чтобы дифф читался
	// как продолжение события, а не как отдельная таблица.
	u.Println("  " + u.emberTail() + " " + body)
}

// itoa — быстрый int → string.
func itoa(n int) string { return fmt.Sprintf("%d", n) }
