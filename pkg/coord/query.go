package coord

import (
	"strconv"
	"strings"
)

// filter accumulates SQL conditions with numbered placeholders. Conditions use
// "?" for each argument; they are renumbered as $1, $2, … in order.
type filter struct {
	conds []string
	args  []any
}

// add appends a condition and its arguments.
func (f *filter) add(cond string, args ...any) {
	for _, a := range args {
		cond = strings.Replace(cond, "?", f.arg(a), 1)
	}
	f.conds = append(f.conds, cond)
}

// arg adds an argument and returns its placeholder, for use outside WHERE.
func (f *filter) arg(v any) string {
	f.args = append(f.args, v)
	return "$" + strconv.Itoa(len(f.args))
}

// where renders " WHERE c1 AND c2 …", or "" without conditions.
func (f *filter) where() string {
	if len(f.conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(f.conds, " AND ")
}

// limit renders " LIMIT $n OFFSET $m" for the page.
func (f *filter) limit(p page) string {
	f.args = append(f.args, p.Size, p.offset())
	n := len(f.args)
	return " LIMIT $" + strconv.Itoa(n-1) + " OFFSET $" + strconv.Itoa(n)
}

// page is a 1-based page request, normalized by newPage.
type page struct {
	Number int
	Size   int
}

func newPage(number, size, defaultSize int) page {
	if number < 1 {
		number = 1
	}
	if size < 1 {
		size = defaultSize
	}
	return page{Number: number, Size: size}
}

func (p page) offset() int { return (p.Number - 1) * p.Size }

func (p page) totalPages(total int) int { return (total + p.Size - 1) / p.Size }
