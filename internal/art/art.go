// Package art draws the picture on the homepage: a field of translucent
// circles with the visit count composited over it as an odometer.
//
// Render is deterministic — the same count always produces the same image,
// down to the byte. That is not incidental. The handler serves images from
// /image/{n}.png with an immutable cache header, which is only honest if n
// fully determines the picture, and it lets the tests assert on exact output
// rather than on vague properties.
package art

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"math/rand/v2"
)

// Width and Height are fixed rather than caller-supplied. The endpoint is
// public and unauthenticated, so a caller-controlled canvas size would be a
// cheap way to make a Raspberry Pi allocate and fill an enormous buffer.
const (
	Width  = 900
	Height = 420
)

// MaxCount is the largest number the odometer can show. Beyond six digits the
// panel no longer fits the canvas, so the handler rejects anything larger
// instead of drawing a mess.
const MaxCount = 999999

const digits = 6

// circleMask is an alpha mask for one soft-edged circle. Implementing
// image.Image and handing it to draw.DrawMask is the standard-library way to
// composite a shape; the one-pixel gradient at the rim is what stops the edges
// looking like staircases.
type circleMask struct {
	cx, cy, r float64
	alpha     float64
}

func (c *circleMask) ColorModel() color.Model { return color.AlphaModel }

func (c *circleMask) Bounds() image.Rectangle {
	return image.Rect(
		int(c.cx-c.r)-1, int(c.cy-c.r)-1,
		int(c.cx+c.r)+1, int(c.cy+c.r)+1,
	)
}

func (c *circleMask) At(x, y int) color.Color {
	dx := float64(x) - c.cx
	dy := float64(y) - c.cy
	d := math.Hypot(dx, dy)
	switch {
	case d <= c.r-1:
		return color.Alpha{uint8(c.alpha)}
	case d >= c.r:
		return color.Alpha{0}
	default:
		// Feather the last pixel of the radius.
		return color.Alpha{uint8(c.alpha * (c.r - d))}
	}
}

// Render draws the image for a given visit count.
func Render(count uint64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, Width, Height))

	// A fixed second parameter keeps the stream a pure function of the count,
	// while the odd constant (golden-ratio derived) scatters neighbouring
	// counts to visibly different colours instead of near-identical ones.
	rng := rand.New(rand.NewPCG(count, 0x9E3779B97F4A7C15))
	baseHue := rng.Float64() * 360

	drawGradient(img, baseHue)
	drawCircles(img, rng, baseHue)
	drawOdometer(img, count)

	return img
}

// drawGradient fills the canvas with a vertical ramp, dark at the bottom so
// the odometer has something quiet to sit on.
func drawGradient(img *image.RGBA, baseHue float64) {
	top := hsl(baseHue, 0.45, 0.22)
	bottom := hsl(math.Mod(baseHue+40, 360), 0.55, 0.06)
	for y := range Height {
		t := float64(y) / float64(Height-1)
		row := color.RGBA{
			R: lerp(top.R, bottom.R, t),
			G: lerp(top.G, bottom.G, t),
			B: lerp(top.B, bottom.B, t),
			A: 255,
		}
		draw.Draw(img, image.Rect(0, y, Width, y+1), &image.Uniform{row}, image.Point{}, draw.Src)
	}
}

func drawCircles(img *image.RGBA, rng *rand.Rand, baseHue float64) {
	const n = 44
	for range n {
		hue := math.Mod(baseHue+rng.Float64()*140-70+360, 360)
		c := hsl(hue, 0.65, 0.35+rng.Float64()*0.35)
		mask := &circleMask{
			cx:    rng.Float64() * Width,
			cy:    rng.Float64() * Height,
			r:     18 + rng.Float64()*90,
			alpha: 28 + rng.Float64()*70,
		}
		draw.DrawMask(img, mask.Bounds(), &image.Uniform{c}, image.Point{}, mask, mask.Bounds().Min, draw.Over)
	}
}

// Odometer geometry, at package scope so the tests can address the panel
// without duplicating the arithmetic.
const (
	cellW  = 62
	cellH  = 104
	gap    = 14
	pad    = 16
	stroke = 10
)

// odometerPanel is the dark plate the digits sit on.
func odometerPanel() image.Rectangle {
	c := digitCell(0)
	last := digitCell(digits - 1)
	return image.Rect(c.Min.X-pad, c.Min.Y-pad, last.Max.X+pad, last.Max.Y+pad)
}

// digitCell is the box the i'th digit is drawn in, counting from the most
// significant.
func digitCell(i int) image.Rectangle {
	totalW := digits*cellW + (digits-1)*gap
	originX := (Width-totalW)/2 + i*(cellW+gap)
	originY := (Height-cellH)/2 + 34
	return image.Rect(originX, originY, originX+cellW, originY+cellH)
}

// drawOdometer composites the zero-padded count over the art.
func drawOdometer(img *image.RGBA, count uint64) {
	totalW := digits*cellW + (digits-1)*gap
	originX := (Width - totalW) / 2
	originY := (Height-cellH)/2 + 34

	// One dark panel behind the whole row, plus a lighter line along the top
	// edge for a bevel. Drawn as two rectangles rather than a rounded box —
	// the extra code for corners is not worth it at this size.
	panel := odometerPanel()
	draw.Draw(img, panel, &image.Uniform{rgba(8, 10, 12, 232)}, image.Point{}, draw.Over)
	bevel := image.Rect(panel.Min.X, panel.Min.Y, panel.Max.X, panel.Min.Y+2)
	draw.Draw(img, bevel, &image.Uniform{rgba(255, 255, 255, 40)}, image.Point{}, draw.Over)

	on := rgba(130, 255, 175, 255)
	off := rgba(130, 255, 175, 30)
	glow := rgba(130, 255, 175, 38)

	if count > MaxCount {
		count = MaxCount
	}
	for i := range digits {
		// Most significant digit first: divide by 10^(digits-1-i).
		p := uint64(1)
		for range digits - 1 - i {
			p *= 10
		}
		d := int((count / p) % 10)

		cell := image.Rect(
			originX+i*(cellW+gap), originY,
			originX+i*(cellW+gap)+cellW, originY+cellH,
		)
		// A slightly larger, very transparent pass under each digit reads as
		// phosphor bleed into the panel around it.
		drawDigit(img, cell.Inset(-3), d, stroke+4, glow, color.RGBA{})
		drawDigit(img, cell, d, stroke, on, off)
	}
}

// hsl converts to RGB. Hue is in degrees, saturation and lightness in [0,1].
// Working in HSL is what lets the palette shift by rotating one number.
func hsl(h, s, l float64) color.RGBA {
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2

	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return color.RGBA{
		R: uint8(math.Round((r + m) * 255)),
		G: uint8(math.Round((g + m) * 255)),
		B: uint8(math.Round((b + m) * 255)),
		A: 255,
	}
}

func lerp(a, b uint8, t float64) uint8 {
	return uint8(math.Round(float64(a) + (float64(b)-float64(a))*t))
}

// rgba builds a translucent colour from straight (non-premultiplied) channels.
//
// image/draw treats color.RGBA as alpha-premultiplied, so writing
// color.RGBA{120, 255, 170, 26} directly is not a dim green — the channels
// exceed the alpha, which is not a valid premultiplied colour at all, and it
// composites to a magenta smear. Every translucent colour in this package goes
// through here so that trap is sprung once, in one place.
func rgba(r, g, b, a uint8) color.RGBA {
	return color.RGBA{
		R: uint8(uint16(r) * uint16(a) / 255),
		G: uint8(uint16(g) * uint16(a) / 255),
		B: uint8(uint16(b) * uint16(a) / 255),
		A: a,
	}
}
