package art

import (
	"image"
	"image/color"
	"image/draw"
)

// Seven-segment glyphs, drawn from filled rectangles rather than a font. That
// keeps the package dependency-free — the standard library ships no font — and
// rectangles are what a real odometer looks like anyway.
//
//	 aaa
//	f   b
//	 ggg
//	e   c
//	 ddd
const (
	segA = 1 << iota // top
	segB             // upper right
	segC             // lower right
	segD             // bottom
	segE             // lower left
	segF             // upper left
	segG             // middle
)

// glyphs maps each decimal digit to the segments it lights.
var glyphs = [10]uint8{
	0: segA | segB | segC | segD | segE | segF,
	1: segB | segC,
	2: segA | segB | segG | segE | segD,
	3: segA | segB | segG | segC | segD,
	4: segF | segG | segB | segC,
	5: segA | segF | segG | segC | segD,
	6: segA | segF | segG | segE | segC | segD,
	7: segA | segB | segC,
	8: segA | segB | segC | segD | segE | segF | segG,
	9: segA | segB | segC | segD | segF | segG,
}

// segmentRects returns the rectangle for each segment of a digit drawn into
// cell, with strokes t pixels thick. Segments are inset by t from the corners
// so neighbouring strokes meet at the joints instead of overlapping into a
// blob.
func segmentRects(cell image.Rectangle, t int) map[uint8]image.Rectangle {
	x0, y0 := cell.Min.X, cell.Min.Y
	x1, y1 := cell.Max.X, cell.Max.Y
	mid := y0 + cell.Dy()/2

	return map[uint8]image.Rectangle{
		segA: {Min: image.Pt(x0+t, y0), Max: image.Pt(x1-t, y0+t)},
		segG: {Min: image.Pt(x0+t, mid-t/2), Max: image.Pt(x1-t, mid+t/2)},
		segD: {Min: image.Pt(x0+t, y1-t), Max: image.Pt(x1-t, y1)},
		segF: {Min: image.Pt(x0, y0+t), Max: image.Pt(x0+t, mid-t/2)},
		segB: {Min: image.Pt(x1-t, y0+t), Max: image.Pt(x1, mid-t/2)},
		segE: {Min: image.Pt(x0, mid+t/2), Max: image.Pt(x0+t, y1-t)},
		segC: {Min: image.Pt(x1-t, mid+t/2), Max: image.Pt(x1, y1-t)},
	}
}

// drawDigit renders one digit into cell. Unlit segments are still drawn, dimly:
// on a real display the whole figure-8 is faintly visible, and it is that ghost
// which makes the lit segments read as lit rather than as floating bars.
func drawDigit(dst draw.Image, cell image.Rectangle, digit int, t int, on, off color.RGBA) {
	lit := glyphs[digit]
	for seg, r := range segmentRects(cell, t) {
		c := off
		if lit&seg != 0 {
			c = on
		}
		draw.Draw(dst, r, &image.Uniform{c}, image.Point{}, draw.Over)
	}
}
