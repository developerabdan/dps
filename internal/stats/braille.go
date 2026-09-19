package stats

import "math"

// A braille cell holds two columns and four rows of dots, so a chart drawn
// with them has twice the width and four times the height of one drawn with
// blocks. The price is colour: a cell prints in one colour, so where two
// series meet they share it.
//
// The bits of a braille character are not in reading order — the fourth row
// was added to the standard later — so dotBit maps the dot at column x, row y
// of a cell to the bit that turns it on.
var dotBit = [2][4]uint8{
	{0x01, 0x02, 0x04, 0x40},
	{0x08, 0x10, 0x20, 0x80},
}

// brailleBase is the character with no dots. A cell's bits are added to it.
const brailleBase = 0x2800

// Grid is a chart drawn as dots: one byte of dots for each cell, addressed
// [row][column] with row 0 at the top.
type Grid [][]uint8

// Dots draws the newest values as a line in a grid width cells wide and
// height cells tall. Two dots go in each cell across, so twice as many values
// fit as there are cells. A history shorter than that is padded on the left,
// so the line grows in from the right as samples arrive.
//
// Consecutive values are joined by the dots between them. Without that a
// value that jumps reads as two unrelated marks rather than a climb.
func Dots(values []float64, width, height int, top float64) Grid {
	g := make(Grid, max(0, height))
	for i := range g {
		g[i] = make([]uint8, max(0, width))
	}
	pw, ph := width*2, height*4
	if pw <= 0 || ph <= 0 {
		return g
	}
	values = Tail(values, pw)
	pad := pw - len(values)
	prev := -1
	for i, v := range values {
		y := (ph - 1) - int(math.Round(ratio(v, top)*float64(ph-1)))
		lo, hi := y, y
		if prev >= 0 {
			lo, hi = min(prev, y), max(prev, y)
		}
		for yy := lo; yy <= hi; yy++ {
			g.set(pad+i, yy)
		}
		prev = y
	}
	return g
}

// At is the dots of one cell, and zero for a cell outside the grid.
func (g Grid) At(row, col int) uint8 {
	if row < 0 || row >= len(g) || col < 0 || col >= len(g[row]) {
		return 0
	}
	return g[row][col]
}

func (g Grid) set(x, y int) {
	if x < 0 || y < 0 {
		return
	}
	row, col := y/4, x/2
	if row >= len(g) || col >= len(g[row]) {
		return
	}
	g[row][col] |= dotBit[x%2][y%4]
}

// Dot is the character for a set of dots. A cell with none prints a space
// rather than the empty braille character, which some fonts draw as a box.
func Dot(mask uint8) rune {
	if mask == 0 {
		return ' '
	}
	return rune(brailleBase + int(mask))
}
