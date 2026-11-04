package imaging

import (
	"image"
	"testing"
)

func TestScale(t *testing.T) {
	if s := Scale(1920, 1080, 1280); s != 1280.0/1920 {
		t.Error(s)
	}
	if s := Scale(800, 600, 1280); s != 1 {
		t.Error(s)
	}
	if s := Scale(1080, 1920, 1280); s != 1280.0/1920 {
		t.Error(s)
	}
}

func TestFromBGRAAndEncode(t *testing.T) {
	pix := []byte{1, 2, 3, 0, 4, 5, 6, 0, 0, 0, 0, 0} // 2x1 with stride padding
	img, err := FromBGRA(2, 1, 12, pix)
	if err != nil {
		t.Fatal(err)
	}
	if got := img.Pix[:8]; got[0] != 3 || got[2] != 1 || got[3] != 255 || got[4] != 6 {
		t.Fatalf("bad conversion %v", got)
	}
	DrawCursor(img, Cursor{Visible: true})
	r := Resize(image.NewRGBA(image.Rect(0, 0, 100, 50)), 0.5)
	if r.Bounds().Dx() != 50 || r.Bounds().Dy() != 25 {
		t.Fatal(r.Bounds())
	}
	if _, mime, err := Encode(r, "png", 0, 0); err != nil || mime != "image/png" {
		t.Fatal(mime, err)
	}
	if _, mime, _ := Encode(r, "png", 0, 10); mime != "image/jpeg" {
		t.Fatal("expected jpeg fallback")
	}
}
