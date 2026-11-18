// Package vnctest is a minimal RFB server for tests: Apple-style version string, ARD (30),
// VNC password (2) or no authentication, ZRLE (all tile subencodings) or Raw updates,
// and a record of the key and pointer events it receives.
package vnctest

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/des"
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"io"
	"math/big"
	"net"
	"sync"
	"testing"
)

type KeyEvent struct {
	Sym  uint32
	Down bool
}

type PointerEvent struct {
	Buttons byte
	X, Y    int
}

type Options struct {
	Width, Height int
	Security      byte // 30 (default), 2 or 1
	User, Pass    string
}

type Server struct {
	Addr string
	opts Options
	ln   net.Listener

	mu       sync.Mutex
	img      []byte // RGB
	Keys     []KeyEvent
	Pointers []PointerEvent
	Encs     []int32
	changed  chan struct{}
}

// Start listens on 127.0.0.1 and serves until the test ends.
func Start(t testing.TB, o Options) *Server {
	t.Helper()
	if o.Width == 0 {
		o.Width, o.Height = 320, 200
	}
	if o.Security == 0 {
		o.Security = 30
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Addr: ln.Addr().String(), opts: o, ln: ln, changed: make(chan struct{}, 1)}
	s.img = Pattern(o.Width, o.Height)
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

// Port returns the listening port.
func (s *Server) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// Pattern is the test image: solid, two-colour, five-colour and gradient areas so that every
// ZRLE subencoding is used.
func Pattern(w, h int) []byte {
	img := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, b byte
			switch {
			case x < w/4:
				r, g, b = 10, 120, 200
			case x < w/2:
				if (x/3+y/3)%2 == 0 {
					r, g, b = 255, 255, 255
				}
			case x < 3*w/4:
				v := byte((x / 7) % 5 * 50)
				r, g, b = v, 255-v, v/2
			default:
				r, g, b = byte(x), byte(y), byte(x+y)
			}
			i := (y*w + x) * 3
			img[i], img[i+1], img[i+2] = r, g, b
		}
	}
	return img
}

// SetPixel changes one pixel and notifies a waiting incremental update request.
func (s *Server) SetPixel(x, y int, r, g, b byte) {
	s.mu.Lock()
	i := (y*s.opts.Width + x) * 3
	s.img[i], s.img[i+1], s.img[i+2] = r, g, b
	s.mu.Unlock()
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// Events returns copies of the recorded input events.
func (s *Server) Events() ([]KeyEvent, []PointerEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]KeyEvent(nil), s.Keys...), append([]PointerEvent(nil), s.Pointers...)
}

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	c.Write([]byte("RFB 003.889\n"))
	ver := make([]byte, 12)
	if _, err := io.ReadFull(r, ver); err != nil {
		return
	}
	c.Write([]byte{1, s.opts.Security})
	sec, err := r.ReadByte()
	if err != nil || sec != s.opts.Security {
		return
	}
	ok := true
	switch sec {
	case 30:
		ok = s.ard(c, r)
	case 2:
		ok = s.vncAuth(c, r)
	}
	if !ok {
		c.Write([]byte{0, 0, 0, 1, 0, 0, 0, 13})
		c.Write([]byte("bad password!"))
		return
	}
	c.Write([]byte{0, 0, 0, 0})
	if _, err := r.ReadByte(); err != nil { // ClientInit
		return
	}
	si := make([]byte, 0, 32)
	si = binary.BigEndian.AppendUint16(si, uint16(s.opts.Width))
	si = binary.BigEndian.AppendUint16(si, uint16(s.opts.Height))
	si = append(si, 32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0)
	si = binary.BigEndian.AppendUint32(si, 4)
	si = append(si, "fake"...)
	c.Write(si)

	var zrle atomicBool
	reqs := make(chan bool, 16)
	done := make(chan struct{})
	defer close(done)
	go s.updates(c, reqs, done, &zrle)
	for {
		t, err := r.ReadByte()
		if err != nil {
			return
		}
		switch t {
		case 0: // SetPixelFormat
			io.CopyN(io.Discard, r, 19)
		case 2: // SetEncodings
			var h struct {
				Pad byte
				N   uint16
			}
			binary.Read(r, binary.BigEndian, &h)
			encs := make([]int32, h.N)
			binary.Read(r, binary.BigEndian, encs)
			s.mu.Lock()
			s.Encs = encs
			s.mu.Unlock()
			for _, e := range encs {
				if e == 16 {
					zrle.set(true)
				}
			}
		case 3: // FramebufferUpdateRequest
			b := make([]byte, 9)
			if _, err := io.ReadFull(r, b); err != nil {
				return
			}
			select {
			case reqs <- b[0] == 1:
			default:
			}
		case 4: // KeyEvent
			b := make([]byte, 7)
			if _, err := io.ReadFull(r, b); err != nil {
				return
			}
			s.mu.Lock()
			s.Keys = append(s.Keys, KeyEvent{Sym: binary.BigEndian.Uint32(b[3:]), Down: b[0] != 0})
			s.mu.Unlock()
		case 5: // PointerEvent
			b := make([]byte, 5)
			if _, err := io.ReadFull(r, b); err != nil {
				return
			}
			s.mu.Lock()
			s.Pointers = append(s.Pointers, PointerEvent{Buttons: b[0],
				X: int(binary.BigEndian.Uint16(b[1:])), Y: int(binary.BigEndian.Uint16(b[3:]))})
			s.mu.Unlock()
		case 6: // ClientCutText
			b := make([]byte, 7)
			if _, err := io.ReadFull(r, b); err != nil {
				return
			}
			io.CopyN(io.Discard, r, int64(binary.BigEndian.Uint32(b[3:])))
		default:
			return
		}
	}
}

type atomicBool struct {
	mu sync.Mutex
	v  bool
}

func (a *atomicBool) set(v bool) { a.mu.Lock(); a.v = v; a.mu.Unlock() }
func (a *atomicBool) get() bool  { a.mu.Lock(); defer a.mu.Unlock(); return a.v }

// updates answers update requests; incremental ones wait for a change.
func (s *Server) updates(c net.Conn, reqs <-chan bool, done <-chan struct{}, zrle *atomicBool) {
	var zw *zlib.Writer
	var zbuf bytes.Buffer
	for {
		var inc bool
		select {
		case inc = <-reqs:
		case <-done:
			return
		}
		if inc {
			select {
			case <-s.changed:
			case <-done:
				return
			}
		}
		s.mu.Lock()
		img := append([]byte(nil), s.img...)
		s.mu.Unlock()
		w, h := s.opts.Width, s.opts.Height
		msg := []byte{0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0} // type, pad, n=1, x, y, w, h
		binary.BigEndian.PutUint16(msg[8:], uint16(w))
		binary.BigEndian.PutUint16(msg[10:], uint16(h))
		if zrle.get() {
			if zw == nil {
				zw = zlib.NewWriter(&zbuf)
			}
			zbuf.Reset()
			encodeZRLE(zw, img, w, h)
			zw.Flush()
			msg = binary.BigEndian.AppendUint32(msg, 16)
			msg = binary.BigEndian.AppendUint32(msg, uint32(zbuf.Len()))
			msg = append(msg, zbuf.Bytes()...)
		} else {
			msg = binary.BigEndian.AppendUint32(msg, 0)
			for i := 0; i < w*h; i++ {
				msg = append(msg, img[i*3+2], img[i*3+1], img[i*3], 0)
			}
		}
		if _, err := c.Write(msg); err != nil {
			return
		}
	}
}

func cstring(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

func (s *Server) ard(c net.Conn, r *bufio.Reader) bool {
	const keyLen = 64
	p, _ := rand.Prime(rand.Reader, keyLen*8)
	g := big.NewInt(2)
	priv, _ := rand.Int(rand.Reader, p)
	pub := new(big.Int).Exp(g, priv, p)
	msg := []byte{0, 2, 0, keyLen}
	msg = append(msg, pad(p.Bytes(), keyLen)...)
	msg = append(msg, pad(pub.Bytes(), keyLen)...)
	c.Write(msg)
	resp := make([]byte, 128+keyLen)
	if _, err := io.ReadFull(r, resp); err != nil {
		return false
	}
	clientPub := new(big.Int).SetBytes(resp[128:])
	shared := new(big.Int).Exp(clientPub, priv, p)
	key := md5.Sum(pad(shared.Bytes(), keyLen))
	blk, _ := aes.NewCipher(key[:])
	creds := make([]byte, 128)
	for i := 0; i < 128; i += 16 {
		blk.Decrypt(creds[i:i+16], resp[i:i+16])
	}
	return cstring(creds[:64]) == s.opts.User && cstring(creds[64:]) == s.opts.Pass
}

func (s *Server) vncAuth(c net.Conn, r *bufio.Reader) bool {
	challenge := make([]byte, 16)
	rand.Read(challenge)
	c.Write(challenge)
	resp := make([]byte, 16)
	if _, err := io.ReadFull(r, resp); err != nil {
		return false
	}
	var key [8]byte
	copy(key[:], s.opts.Pass)
	for i := range key { // DES key bytes are bit-reversed in VNC authentication
		var rev byte
		for j := 0; j < 8; j++ {
			rev = rev<<1 | (key[i]>>j)&1
		}
		key[i] = rev
	}
	blk, _ := des.NewCipher(key[:])
	want := make([]byte, 16)
	blk.Encrypt(want[:8], challenge[:8])
	blk.Encrypt(want[8:], challenge[8:])
	return bytes.Equal(want, resp)
}

func pad(b []byte, n int) []byte {
	if len(b) >= n {
		return b
	}
	return append(make([]byte, n-len(b)), b...)
}

// encodeZRLE writes the rectangle's tiles, cycling subencodings so all decoder paths run.
func encodeZRLE(w io.Writer, img []byte, width, height int) {
	tileNo := 0
	for ty := 0; ty < height; ty += 64 {
		th := min(64, height-ty)
		for tx := 0; tx < width; tx += 64 {
			tw := min(64, width-tx)
			var px [][3]byte
			for y := ty; y < ty+th; y++ {
				for x := tx; x < tx+tw; x++ {
					i := (y*width + x) * 3
					// CPIXEL: B G R
					px = append(px, [3]byte{img[i+2], img[i+1], img[i]})
				}
			}
			writeTile(w, px, tw, th, tileNo)
			tileNo++
		}
	}
}

func writeTile(w io.Writer, px [][3]byte, tw, th, n int) {
	idx := map[[3]byte]int{}
	var pal [][3]byte
	for _, p := range px {
		if _, ok := idx[p]; !ok {
			idx[p] = len(pal)
			pal = append(pal, p)
		}
	}
	switch {
	case len(pal) == 1:
		w.Write([]byte{1})
		w.Write(pal[0][:])
	case len(pal) <= 16 && n%2 == 0: // packed palette
		w.Write([]byte{byte(len(pal))})
		for _, p := range pal {
			w.Write(p[:])
		}
		bits := 4
		switch {
		case len(pal) == 2:
			bits = 1
		case len(pal) <= 4:
			bits = 2
		}
		for y := 0; y < th; y++ {
			row := make([]byte, (tw*bits+7)/8)
			for x := 0; x < tw; x++ {
				b := x * bits
				row[b/8] |= byte(idx[px[y*tw+x]]) << (8 - bits - b%8)
			}
			w.Write(row)
		}
	case len(pal) <= 127 && len(pal) > 1 && n%2 == 1: // palette RLE
		w.Write([]byte{byte(128 + len(pal))})
		for _, p := range pal {
			w.Write(p[:])
		}
		for i := 0; i < len(px); {
			j := i
			for j < len(px) && px[j] == px[i] {
				j++
			}
			if j-i == 1 {
				w.Write([]byte{byte(idx[px[i]])})
			} else {
				w.Write([]byte{byte(idx[px[i]]) | 128})
				writeRun(w, j-i)
			}
			i = j
		}
	case n%2 == 0: // raw
		w.Write([]byte{0})
		for _, p := range px {
			w.Write(p[:])
		}
	default: // plain RLE
		w.Write([]byte{128})
		for i := 0; i < len(px); {
			j := i
			for j < len(px) && px[j] == px[i] {
				j++
			}
			w.Write(px[i][:])
			writeRun(w, j-i)
			i = j
		}
	}
}

func writeRun(w io.Writer, n int) {
	n--
	for n >= 255 {
		w.Write([]byte{255})
		n -= 255
	}
	w.Write([]byte{byte(n)})
}
