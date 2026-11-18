package vnc

import (
	"bufio"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
)

// zrleReader decodes ZRLE rectangles (RFC 6143 §7.7.6). All rectangles of a connection share
// one zlib stream; the server flushes after each rectangle, so each rectangle's data is
// self-sufficient once appended to the stream.
//
// Pixels are 32bpp true colour with all colour bits in the low three bytes, so a CPIXEL is
// three bytes (B, G, R) and output pixels are B G R X.
type zrleReader struct {
	src chunkSource
	z   io.ReadCloser
	br  *bufio.Reader
}

// chunkSource feeds the zlib stream. It implements io.ByteReader so the flate decoder does
// not wrap it in its own read-ahead buffer (which would hit EOF between rectangles).
type chunkSource struct{ buf []byte }

func (s *chunkSource) add(b []byte) {
	if len(s.buf) == 0 {
		s.buf = append(s.buf[:0], b...)
		return
	}
	s.buf = append(s.buf, b...)
}

func (s *chunkSource) Read(p []byte) (int, error) {
	if len(s.buf) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

func (s *chunkSource) ReadByte() (byte, error) {
	if len(s.buf) == 0 {
		return 0, io.EOF
	}
	b := s.buf[0]
	s.buf = s.buf[1:]
	return b, nil
}

const zrleTile = 64

func (z *zrleReader) decode(data []byte, x, y, w, h int, paint func(tx, ty, tw, th int, pix []byte)) error {
	z.src.add(data)
	if z.z == nil {
		zr, err := zlib.NewReader(&z.src)
		if err != nil {
			return err
		}
		z.z = zr
		z.br = bufio.NewReaderSize(zr, 64<<10)
	}
	tile := make([]byte, zrleTile*zrleTile*4)
	for ty := y; ty < y+h; ty += zrleTile {
		th := min(zrleTile, y+h-ty)
		for tx := x; tx < x+w; tx += zrleTile {
			tw := min(zrleTile, x+w-tx)
			pix := tile[:tw*th*4]
			if err := z.tile(pix, tw, th); err != nil {
				return err
			}
			paint(tx, ty, tw, th, pix)
		}
	}
	return nil
}

func (z *zrleReader) cpixel(dst []byte) error {
	if _, err := io.ReadFull(z.br, dst[:3]); err != nil {
		return err
	}
	dst[3] = 0xFF
	return nil
}

func (z *zrleReader) runLength() (int, error) {
	n := 1
	for {
		b, err := z.br.ReadByte()
		if err != nil {
			return 0, err
		}
		n += int(b)
		if b != 255 {
			return n, nil
		}
	}
}

func (z *zrleReader) tile(pix []byte, w, h int) error {
	sub, err := z.br.ReadByte()
	if err != nil {
		return err
	}
	total := w * h
	switch {
	case sub == 0: // raw CPIXELs
		for i := 0; i < total; i++ {
			if err := z.cpixel(pix[i*4:]); err != nil {
				return err
			}
		}
	case sub == 1: // solid
		var c [4]byte
		if err := z.cpixel(c[:]); err != nil {
			return err
		}
		for i := 0; i < total; i++ {
			copy(pix[i*4:], c[:])
		}
	case sub >= 2 && sub <= 16: // packed palette
		pal, err := z.palette(int(sub))
		if err != nil {
			return err
		}
		bits := 4
		switch {
		case sub == 2:
			bits = 1
		case sub <= 4:
			bits = 2
		}
		rowBytes := (w*bits + 7) / 8
		row := make([]byte, rowBytes)
		mask := byte(1<<bits - 1)
		for yy := 0; yy < h; yy++ {
			if _, err := io.ReadFull(z.br, row); err != nil {
				return err
			}
			for xx := 0; xx < w; xx++ {
				bit := xx * bits
				idx := (row[bit/8] >> (8 - bits - bit%8)) & mask
				if int(idx) >= len(pal) {
					return fmt.Errorf("palette index %d out of range", idx)
				}
				copy(pix[(yy*w+xx)*4:], pal[idx][:])
			}
		}
	case sub == 128: // plain RLE
		for i := 0; i < total; {
			var c [4]byte
			if err := z.cpixel(c[:]); err != nil {
				return err
			}
			n, err := z.runLength()
			if err != nil {
				return err
			}
			if i+n > total {
				return errors.New("RLE run overflows tile")
			}
			for ; n > 0; n-- {
				copy(pix[i*4:], c[:])
				i++
			}
		}
	case sub >= 130: // palette RLE
		pal, err := z.palette(int(sub) - 128)
		if err != nil {
			return err
		}
		for i := 0; i < total; {
			b, err := z.br.ReadByte()
			if err != nil {
				return err
			}
			n := 1
			if b&128 != 0 {
				b &= 127
				if n, err = z.runLength(); err != nil {
					return err
				}
			}
			if int(b) >= len(pal) {
				return fmt.Errorf("palette index %d out of range", b)
			}
			if i+n > total {
				return errors.New("RLE run overflows tile")
			}
			for ; n > 0; n-- {
				copy(pix[i*4:], pal[b][:])
				i++
			}
		}
	default:
		return fmt.Errorf("invalid tile subencoding %d", sub)
	}
	return nil
}

func (z *zrleReader) palette(n int) ([][4]byte, error) {
	pal := make([][4]byte, n)
	for i := range pal {
		if err := z.cpixel(pal[i][:]); err != nil {
			return nil, err
		}
	}
	return pal, nil
}
