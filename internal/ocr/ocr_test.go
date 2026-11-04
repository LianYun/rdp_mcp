package ocr

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

func TestParseTSV(t *testing.T) {
	tsv := "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n" +
		"5\t1\t1\t1\t1\t1\t100\t50\t40\t12\t90\tHello\n" +
		"5\t1\t1\t1\t1\t2\t150\t51\t50\t12\t80\tWorld\n" +
		"5\t1\t2\t1\t1\t1\t10\t10\t20\t14\t95\t你好\n" +
		"5\t1\t2\t1\t1\t2\t32\t10\t20\t14\t95\t世界\n" +
		"5\t1\t3\t1\t1\t1\t10\t200\t20\t14\t-1\t \n"
	lines, err := parseTSV([]byte(tsv))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("%+v", lines)
	}
	if lines[0].Text != "你好世界" || lines[1].Text != "Hello World" {
		t.Fatalf("order/join: %+v", lines)
	}
	if lines[1].Box != image.Rect(100, 50, 200, 63) || lines[1].Conf != 85 {
		t.Fatalf("box/conf: %+v", lines[1])
	}
}

func TestRecognizeWithTesseract(t *testing.T) {
	if _, err := exec.LookPath("tesseract"); err != nil {
		t.Skip("tesseract not installed")
	}
	img := image.NewRGBA(image.Rect(0, 0, 400, 80))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	d := &font.Drawer{Dst: img, Src: image.NewUniform(color.Black), Face: basicfont.Face7x13,
		Dot: fixed.P(20, 40)}
	d.DrawString("OPEN SETTINGS")
	// upscale 3x so the tiny bitmap font is readable
	big := image.NewRGBA(image.Rect(0, 0, 1200, 240))
	for y := 0; y < 240; y++ {
		for x := 0; x < 1200; x++ {
			big.Set(x, y, img.At(x/3, y/3))
		}
	}
	lines, err := (&Engine{}).Recognize(context.Background(), big)
	if err != nil {
		t.Fatal(err)
	}
	var all []string
	for _, l := range lines {
		all = append(all, l.Text)
	}
	if !strings.Contains(strings.ToUpper(strings.Join(all, " ")), "SETTINGS") {
		t.Fatalf("got %q", all)
	}
}
