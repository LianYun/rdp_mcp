// Package ocr runs tesseract on screenshots on the MCP host (docs/design.md D16).
package ocr

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
)

// Line is one recognized text line in source image pixel coordinates.
type Line struct {
	Text string          `json:"text"`
	Box  image.Rectangle `json:"-"`
	Conf float64         `json:"conf"`
}

// Engine wraps a tesseract binary.
type Engine struct {
	Path      string // tesseract executable; empty = look up in PATH
	Languages string // e.g. "chi_sim+eng"; empty = auto

	once     sync.Once
	resolved string
	langs    string
	initErr  error
}

// ErrNotInstalled is returned when tesseract cannot be found.
var ErrNotInstalled = errors.New("OCR needs tesseract on the machine running rdp-mcp " +
	"(macOS: brew install tesseract tesseract-lang; Debian/Ubuntu: apt install tesseract-ocr tesseract-ocr-chi-sim)")

func (e *Engine) init() {
	p := e.Path
	if p == "" {
		p = "tesseract"
	}
	path, err := exec.LookPath(p)
	if err != nil {
		e.initErr = ErrNotInstalled
		return
	}
	e.resolved = path
	e.langs = e.Languages
	if e.langs == "" {
		e.langs = autoLanguages(path)
	}
}

// autoLanguages prefers Chinese + English when the Chinese pack is installed.
func autoLanguages(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--list-langs").CombinedOutput()
	if err != nil {
		return "eng"
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		have[strings.TrimSpace(l)] = true
	}
	var langs []string
	for _, l := range []string{"chi_sim", "chi_tra"} {
		if have[l] {
			langs = append(langs, l)
		}
	}
	if have["eng"] || len(langs) == 0 {
		langs = append(langs, "eng")
	}
	return strings.Join(langs, "+")
}

// Languages returns the effective language list (after auto-detection).
func (e *Engine) EffectiveLanguages() (string, error) {
	e.once.Do(e.init)
	return e.langs, e.initErr
}

// Recognize returns the text lines found in img, top-to-bottom, left-to-right.
func (e *Engine) Recognize(ctx context.Context, img image.Image) ([]Line, error) {
	e.once.Do(e.init)
	if e.initErr != nil {
		return nil, e.initErr
	}
	// Screen text at 96 DPI is small for tesseract; upscaling 2x improves accuracy a lot.
	factor := 1
	b := img.Bounds()
	if max(b.Dx(), b.Dy()) <= 2400 {
		factor = 2
		up := image.NewRGBA(image.Rect(0, 0, b.Dx()*2, b.Dy()*2))
		xdraw.CatmullRom.Scale(up, up.Bounds(), img, b, xdraw.Src, nil)
		img = up
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// psm 11: sparse text, suits screenshots with scattered UI labels.
	cmd := exec.CommandContext(ctx, e.resolved, "stdin", "stdout", "-l", e.langs, "--psm", "11", "tsv")
	cmd.Stdin = &buf
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tesseract: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	lines, err := parseTSV(out)
	if err != nil {
		return nil, err
	}
	for i := range lines {
		r := lines[i].Box
		lines[i].Box = image.Rect(r.Min.X/factor, r.Min.Y/factor, (r.Max.X+factor-1)/factor, (r.Max.Y+factor-1)/factor).Add(b.Min)
	}
	return lines, nil
}

type lineKey struct{ page, block, par, line int }

// parseTSV groups tesseract word rows (level 5) into lines.
func parseTSV(data []byte) ([]Line, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = '\t'
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	type acc struct {
		words []string
		box   image.Rectangle
		conf  float64
		n     int
	}
	lines := map[lineKey]*acc{}
	var order []lineKey
	header := true
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse tesseract output: %w", err)
		}
		if header {
			header = false
			continue
		}
		if len(rec) < 12 || rec[0] != "5" {
			continue
		}
		text := strings.TrimSpace(rec[11])
		conf, _ := strconv.ParseFloat(rec[10], 64)
		if text == "" || conf < 0 {
			continue
		}
		n := func(i int) int { v, _ := strconv.Atoi(rec[i]); return v }
		k := lineKey{n(1), n(2), n(3), n(4)}
		box := image.Rect(n(6), n(7), n(6)+n(8), n(7)+n(9))
		a := lines[k]
		if a == nil {
			a = &acc{box: box}
			lines[k] = a
			order = append(order, k)
		} else {
			a.box = a.box.Union(box)
		}
		a.words = append(a.words, text)
		a.conf += conf
		a.n++
	}
	out := make([]Line, 0, len(order))
	for _, k := range order {
		a := lines[k]
		out = append(out, Line{Text: joinWords(a.words), Box: a.box, Conf: a.conf / float64(a.n)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Box, out[j].Box
		if abs(a.Min.Y-b.Min.Y) > min(a.Dy(), b.Dy())/2 {
			return a.Min.Y < b.Min.Y
		}
		return a.Min.X < b.Min.X
	})
	return out, nil
}

// joinWords joins words with spaces, except between CJK characters.
func joinWords(words []string) string {
	var b strings.Builder
	for i, w := range words {
		if i > 0 && !(isCJK(lastRune(words[i-1])) && isCJK(firstRune(w))) {
			b.WriteByte(' ')
		}
		b.WriteString(w)
	}
	return b.String()
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

func lastRune(s string) rune {
	rs := []rune(s)
	if len(rs) == 0 {
		return 0
	}
	return rs[len(rs)-1]
}

func isCJK(r rune) bool {
	return (r >= 0x3000 && r <= 0x9FFF) || (r >= 0xF900 && r <= 0xFAFF) || (r >= 0xFF00 && r <= 0xFFEF)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
