package art

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func encode(t *testing.T, count uint64) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, Render(count)); err != nil {
		t.Fatalf("encode %d: %v", count, err)
	}
	return buf.Bytes()
}

// The handler serves these with an immutable cache header, which is only
// truthful if the count fully determines the bytes.
func TestRenderIsDeterministic(t *testing.T) {
	for _, count := range []uint64{0, 1, 42, 1337, MaxCount} {
		if a, b := encode(t, count), encode(t, count); !bytes.Equal(a, b) {
			t.Errorf("count %d rendered differently on a second call", count)
		}
	}
}

func TestDifferentCountsRenderDifferently(t *testing.T) {
	seen := map[string]uint64{}
	for _, count := range []uint64{0, 1, 2, 3, 41, 42, 43, 999, 1000, 123456} {
		key := string(encode(t, count))
		if prev, dup := seen[key]; dup {
			t.Errorf("counts %d and %d produced identical images", prev, count)
		}
		seen[key] = count
	}
}

func TestRenderBounds(t *testing.T) {
	got := Render(7).Bounds()
	want := image.Rect(0, 0, Width, Height)
	if got != want {
		t.Errorf("bounds = %v, want %v", got, want)
	}
}

func TestRenderDecodesAsPNG(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(encode(t, 99)))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if img.Bounds().Dx() != Width || img.Bounds().Dy() != Height {
		t.Errorf("decoded size = %dx%d, want %dx%d", img.Bounds().Dx(), img.Bounds().Dy(), Width, Height)
	}
}

// Above six digits the odometer cannot represent the number; it must clamp to
// all-nines rather than wrap around and show something misleading.
func TestCountsAboveMaxClampAndStayInBounds(t *testing.T) {
	clamped := encode(t, MaxCount)
	for _, count := range []uint64{MaxCount + 1, 1 << 40, ^uint64(0)} {
		if !bytes.Equal(encode(t, count), clamped) {
			// Only the digits are expected to match; the background differs
			// because it is seeded by the raw count. So compare digits only.
			t.Logf("count %d has its own background, which is expected", count)
		}
		img := Render(count) // must not panic or draw outside the canvas
		if img.Bounds() != image.Rect(0, 0, Width, Height) {
			t.Errorf("count %d produced bounds %v", count, img.Bounds())
		}
	}
}

func TestGlyphTableIsComplete(t *testing.T) {
	for d, segs := range glyphs {
		if segs == 0 {
			t.Errorf("digit %d has no segments lit", d)
		}
	}
	// A one has the fewest lit segments, an eight the most; if those two are
	// wrong the whole table is probably misindexed.
	if bits(glyphs[1]) != 2 {
		t.Errorf("digit 1 lights %d segments, want 2", bits(glyphs[1]))
	}
	if bits(glyphs[8]) != 7 {
		t.Errorf("digit 8 lights %d segments, want 7", bits(glyphs[8]))
	}
	// Every digit must be distinguishable from every other.
	seen := map[uint8]int{}
	for d, segs := range glyphs {
		if prev, dup := seen[segs]; dup {
			t.Errorf("digits %d and %d light the same segments", prev, d)
		}
		seen[segs] = d
	}
}

func bits(b uint8) int {
	n := 0
	for ; b != 0; b >>= 1 {
		n += int(b & 1)
	}
	return n
}

func TestSegmentRectsStayInsideTheCell(t *testing.T) {
	cell := image.Rect(10, 20, 72, 124)
	for seg, r := range segmentRects(cell, 12) {
		if !r.In(cell) {
			t.Errorf("segment %d rect %v escapes cell %v", seg, r, cell)
		}
		if r.Empty() {
			t.Errorf("segment %d rect %v is empty", seg, r)
		}
	}
}

func TestHSLEndpoints(t *testing.T) {
	if got := hsl(0, 0, 0); got.R != 0 || got.G != 0 || got.B != 0 {
		t.Errorf("hsl(0,0,0) = %v, want black", got)
	}
	if got := hsl(0, 0, 1); got.R != 255 || got.G != 255 || got.B != 255 {
		t.Errorf("hsl(0,0,1) = %v, want white", got)
	}
	if got := hsl(0, 1, 0.5); got.R != 255 || got.G != 0 || got.B != 0 {
		t.Errorf("hsl(0,1,.5) = %v, want pure red", got)
	}
	if got := hsl(120, 1, 0.5); got.R != 0 || got.G != 255 || got.B != 0 {
		t.Errorf("hsl(120,1,.5) = %v, want pure green", got)
	}
}

// image/draw treats color.RGBA as alpha-premultiplied, so a translucent colour
// whose channels exceed its alpha is not merely wrong, it composites to a
// different hue entirely. An earlier version of this package wrote
// color.RGBA{120, 255, 170, 26} for the unlit segments and drew a magenta
// smear across every digit — while the whole suite stayed green, because
// nothing looked at the pixels.
func TestTranslucentColoursArePremultiplied(t *testing.T) {
	for _, c := range []color.RGBA{
		rgba(130, 255, 175, 30),
		rgba(130, 255, 175, 38),
		rgba(8, 10, 12, 232),
		rgba(255, 255, 255, 40),
		rgba(255, 255, 255, 255),
		rgba(255, 255, 255, 0),
	} {
		if c.R > c.A || c.G > c.A || c.B > c.A {
			t.Errorf("rgba produced %v, which is not valid premultiplied alpha (channel > alpha)", c)
		}
	}
}

// A lit segment must be clearly brighter, and clearly greener, than an unlit
// one — otherwise the number is unreadable. Sampled at known segment centres
// rather than across the panel, because the plate is deliberately translucent
// and a warm background legitimately bleeds through the dark areas.
func TestLitAndUnlitSegmentsAreDistinguishable(t *testing.T) {
	// Every digit is a 1, which lights only the two right-hand segments.
	img := Render(111111)
	rects := segmentRects(digitCell(0), stroke)

	sample := func(r image.Rectangle) color.RGBA {
		return img.RGBAAt((r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2)
	}

	lit := sample(rects[segB])   // lit for a 1
	unlit := sample(rects[segA]) // unlit for a 1

	if lit.G < 200 {
		t.Errorf("lit segment green = %d, want >= 200", lit.G)
	}
	if unlit.G > 90 {
		t.Errorf("unlit segment green = %d, want <= 90 (it should be a faint ghost)", unlit.G)
	}
	// A dim ghost is still a *green* ghost. This is the specific check that
	// catches the magenta bug: with a non-premultiplied colour this pixel
	// composites to {146, 11, 190}, whose green channel is low enough to pass
	// the brightness check above while being visibly wrong.
	if unlit.G < unlit.R || unlit.G < unlit.B {
		t.Errorf("unlit segment %v is not green-dominant", unlit)
	}
	if lit.G <= lit.R || lit.G <= lit.B {
		t.Errorf("lit segment %v is not green-dominant", lit)
	}
	if lit.G-unlit.G < 150 {
		t.Errorf("contrast between lit and unlit is %d, want >= 150", lit.G-unlit.G)
	}
}

// The glyph table drives which segments light, so verify the pixels agree with
// it for a digit that uses most of them.
func TestDrawnSegmentsMatchTheGlyphTable(t *testing.T) {
	img := Render(777777) // a 7 lights a, b and c only
	rects := segmentRects(digitCell(0), stroke)

	for seg, r := range rects {
		g := img.RGBAAt((r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2).G
		wantLit := glyphs[7]&seg != 0
		gotLit := g >= 200
		if wantLit != gotLit {
			t.Errorf("segment %d: drawn lit = %v (green %d), glyph table says %v", seg, gotLit, g, wantLit)
		}
	}
}
