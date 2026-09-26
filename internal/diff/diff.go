// Package diff produces line diffs for showing pending changes and backups.
package diff

import "strings"

// Line is one line of a diff.
type Line struct {
	Op   string `json:"op"` // " " same, "-" removed, "+" added
	Text string `json:"text"`
	Old  int    `json:"old,omitempty"` // 1-based line number in a (0 if added)
	New  int    `json:"new,omitempty"` // 1-based line number in b (0 if removed)
}

func split(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// Lines returns the full line diff of a and b.
func Lines(a, b string) []Line {
	x, y := split(a), split(b)
	// trim common prefix/suffix to keep the table small
	pre := 0
	for pre < len(x) && pre < len(y) && x[pre] == y[pre] {
		pre++
	}
	suf := 0
	for suf < len(x)-pre && suf < len(y)-pre && x[len(x)-1-suf] == y[len(y)-1-suf] {
		suf++
	}
	mx, my := x[pre:len(x)-suf], y[pre:len(y)-suf]

	var out []Line
	for i := 0; i < pre; i++ {
		out = append(out, Line{Op: " ", Text: x[i], Old: i + 1, New: i + 1})
	}
	n, m := len(mx), len(my)
	if n*m > 25_000_000 {
		// too big to align; show as replace
		for i, t := range mx {
			out = append(out, Line{Op: "-", Text: t, Old: pre + i + 1})
		}
		for j, t := range my {
			out = append(out, Line{Op: "+", Text: t, New: pre + j + 1})
		}
	} else {
		lcs := make([][]int32, n+1)
		for i := range lcs {
			lcs[i] = make([]int32, m+1)
		}
		for i := n - 1; i >= 0; i-- {
			for j := m - 1; j >= 0; j-- {
				if mx[i] == my[j] {
					lcs[i][j] = lcs[i+1][j+1] + 1
				} else if lcs[i+1][j] >= lcs[i][j+1] {
					lcs[i][j] = lcs[i+1][j]
				} else {
					lcs[i][j] = lcs[i][j+1]
				}
			}
		}
		i, j := 0, 0
		for i < n || j < m {
			switch {
			case i < n && j < m && mx[i] == my[j]:
				out = append(out, Line{Op: " ", Text: mx[i], Old: pre + i + 1, New: pre + j + 1})
				i++
				j++
			case i < n && (j >= m || lcs[i+1][j] >= lcs[i][j+1]):
				out = append(out, Line{Op: "-", Text: mx[i], Old: pre + i + 1})
				i++
			default:
				out = append(out, Line{Op: "+", Text: my[j], New: pre + j + 1})
				j++
			}
		}
	}
	for k := 0; k < suf; k++ {
		out = append(out, Line{Op: " ", Text: x[len(x)-suf+k], Old: len(x) - suf + k + 1, New: len(y) - suf + k + 1})
	}
	return out
}

// Hunks keeps only changed lines plus `context` lines around them. A line
// with Op "~" marks skipped unchanged lines.
func Hunks(lines []Line, context int) []Line {
	keep := make([]bool, len(lines))
	for i, l := range lines {
		if l.Op != " " {
			for k := i - context; k <= i+context; k++ {
				if k >= 0 && k < len(lines) {
					keep[k] = true
				}
			}
		}
	}
	var out []Line
	skipped := false
	for i, l := range lines {
		if keep[i] {
			out = append(out, l)
			skipped = false
		} else if !skipped {
			out = append(out, Line{Op: "~"})
			skipped = true
		}
	}
	return out
}

// Stats counts added and removed lines.
func Stats(lines []Line) (added, removed int) {
	for _, l := range lines {
		switch l.Op {
		case "+":
			added++
		case "-":
			removed++
		}
	}
	return
}
