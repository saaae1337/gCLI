package tui

import "strings"

// ---------- Дифф для подтверждения ----------
//
// В модальном окне записи показывается не весь файл, а только места
// изменений с парой строк контекста: решение принимается по ним, а
// полотно на сотни строк только прячет кнопки.

// diffOp — строка диффа: ' ' — контекст, '-' — удалено, '+' — добавлено.
type diffOp struct {
	Kind byte
	Text string
}

// lineDiff — построчный дифф двух текстов.
func lineDiff(oldS, newS string) []diffOp {
	a := splitLines(oldS)
	b := splitLines(newS)
	const maxN = 500
	if len(a) > maxN || len(b) > maxN {
		// Слишком большие файлы: не строим матрицу, показываем начало.
		return []diffOp{
			{'+', "файл большой: показаны первые строки"},
			{'+', strings.Join(b[:minInt(len(b), 8)], "\n")},
		}
	}
	// LCS-таблица.
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
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
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

// diffWindow — оставить только изменения с контекстом.
//
// Возвращает не больше maxLines строк. Если изменений нет — одну
// контекстную строку: «файл не изменился» тоже полезно увидеть.
func diffWindow(ops []diffOp, maxLines int) []diffOp {
	// Отмечаем, какие строки показывать: изменения и context вокруг.
	keep := make([]bool, len(ops))
	changed := false
	for i, op := range ops {
		if op.Kind != ' ' {
			changed = true
			for k := i - 2; k <= i+2; k++ {
				if k >= 0 && k < len(ops) {
					keep[k] = true
				}
			}
		}
	}
	if !changed {
		return []diffOp{{' ', "изменений нет"}}
	}
	var out []diffOp
	skipped := 0
	for i, op := range ops {
		if keep[i] {
			if skipped > 0 {
				out = append(out, diffOp{' ', "⋯"})
				skipped = 0
			}
			out = append(out, op)
			if len(out) >= maxLines {
				out = append(out, diffOp{' ', "…"})
				break
			}
		} else {
			skipped++
		}
	}
	return out
}

// splitLines — строки текста.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
