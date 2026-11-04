// Package imaging converts worker frames into scaled, encoded screenshots.
package imaging

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"

	xdraw "golang.org/x/image/draw"
)

// FromBGRA converts a BGRA buffer into an RGBA image (alpha forced opaque).
func FromBGRA(w, h, stride int, pix []byte) (*image.RGBA, error) {
	if w <= 0 || h <= 0 || stride < w*4 || len(pix) < stride*(h-1)+w*4 {
		return nil, fmt.Errorf("invalid frame %dx%d stride %d len %d", w, h, stride, len(pix))
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		src := pix[y*stride : y*stride+w*4]
		dst := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for i := 0; i < len(src); i += 4 {
			dst[i] = src[i+2]
			dst[i+1] = src[i+1]
			dst[i+2] = src[i]
			dst[i+3] = 0xFF
		}
	}
	return img, nil
}

// BGRAToNRGBA converts a BGRA buffer with real alpha (cursor shapes).
func BGRAToNRGBA(w, h, stride int, pix []byte) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s := pix[y*stride+x*4:]
			d := img.Pix[y*img.Stride+x*4:]
			d[0], d[1], d[2], d[3] = s[2], s[1], s[0], s[3]
		}
	}
	return img
}

// Cursor describes a pointer to overlay.
type Cursor struct {
	X, Y       int
	HotX, HotY int
	Shape      image.Image // nil = default arrow
	Visible    bool
}

var defaultArrow = func() *image.NRGBA {
	rows := []string{
		"X...........",
		"XX..........",
		"X#X.........",
		"X##X........",
		"X###X.......",
		"X####X......",
		"X#####X.....",
		"X######X....",
		"X#######X...",
		"X########X..",
		"X#####XXXXX.",
		"X##X##X.....",
		"X#X.X##X....",
		"XX..X##X....",
		"X....X##X...",
		".....X##X...",
		"......XX....",
	}
	img := image.NewNRGBA(image.Rect(0, 0, len(rows[0]), len(rows)))
	for y, r := range rows {
		for x, ch := range r {
			switch ch {
			case 'X':
				img.Set(x, y, color.NRGBA{0, 0, 0, 255})
			case '#':
				img.Set(x, y, color.NRGBA{255, 255, 255, 255})
			}
		}
	}
	return img
}()

// DrawCursor composites the cursor onto img.
func DrawCursor(img *image.RGBA, c Cursor) {
	if !c.Visible {
		return
	}
	shape := c.Shape
	hx, hy := c.HotX, c.HotY
	if shape == nil {
		shape, hx, hy = defaultArrow, 0, 0
	}
	b := shape.Bounds()
	r := image.Rect(c.X-hx, c.Y-hy, c.X-hx+b.Dx(), c.Y-hy+b.Dy())
	draw.Draw(img, r, shape, b.Min, draw.Over)
}

// Scale returns the factor (<=1) that fits the long side into maxSide.
func Scale(w, h, maxSide int) float64 {
	long := w
	if h > long {
		long = h
	}
	if maxSide <= 0 || long <= maxSide {
		return 1
	}
	return float64(maxSide) / float64(long)
}

// Resize scales img by s.
func Resize(img image.Image, s float64) image.Image {
	if s >= 1 {
		return img
	}
	b := img.Bounds()
	w := int(float64(b.Dx())*s + 0.5)
	h := int(float64(b.Dy())*s + 0.5)
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	return dst
}

// Crop returns a sub-image (clamped to bounds).
func Crop(img *image.RGBA, r image.Rectangle) *image.RGBA {
	r = r.Intersect(img.Bounds())
	if r.Empty() {
		return img
	}
	return img.SubImage(r).(*image.RGBA)
}

// Encode encodes img as png or jpeg. If png exceeds maxPNG bytes, falls back to jpeg.
func Encode(img image.Image, format string, quality int, maxPNG int) ([]byte, string, error) {
	var buf bytes.Buffer
	if format != "jpeg" && format != "jpg" {
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		if err := enc.Encode(&buf, img); err != nil {
			return nil, "", err
		}
		if maxPNG <= 0 || buf.Len() <= maxPNG {
			return buf.Bytes(), "image/png", nil
		}
		buf.Reset()
	}
	if quality <= 0 {
		quality = 85
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "image/jpeg", nil
}
