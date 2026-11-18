// Package vnc is the RFB (VNC) client used for macOS targets (docs/design.md D21, §5a)
// and the `remoter-mcp vnc-worker` process that exposes it over the worker IPC protocol.
package vnc

import (
	"bufio"
	"context"
	"crypto/aes"
	"crypto/des"
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strconv"
	"sync"
	"time"
)

// Security types (RFC 6143 §7.1.2; 30 is Apple Remote Desktop).
const (
	secNone    = 1
	secVNCAuth = 2
	secARD     = 30
)

// Encodings.
const (
	encRaw         int32 = 0
	encCopyRect    int32 = 1
	encZRLE        int32 = 16
	encDesktopSize int32 = -223
	encLastRect    int32 = -224
)

// Options are the connection parameters.
type Options struct {
	Host     string
	Port     int
	Username string // required for Apple (ARD) authentication
	Password string
	Timeout  time.Duration // handshake + first frame
}

// AuthError reports rejected credentials.
type AuthError struct{ Reason string }

func (e *AuthError) Error() string {
	msg := "VNC authentication failed"
	if e.Reason != "" {
		msg += ": " + e.Reason
	}
	return msg + " (check username/password and that the account may use Screen Sharing)"
}

// Client is a connected RFB session. The framebuffer is kept current by requesting an
// incremental update as soon as the previous one arrives.
type Client struct {
	conn net.Conn
	r    *bufio.Reader
	wmu  sync.Mutex

	Name    string
	Version string

	mu         sync.Mutex
	width      int
	height     int
	fb         []byte // BGRX, stride width*4
	dirtySeq   int
	lastChange time.Time
	firstFrame time.Time
	buttons    byte
	ptrX, ptrY int

	zr zrleReader

	// Callbacks run on the read goroutine (outside c.mu).
	OnUpdate func()
	OnResize func(w, h int)

	firstCh   chan struct{}
	firstOnce sync.Once
	done      chan struct{}
	errMu     sync.Mutex
	err       error
}

// Dial connects, authenticates and waits for the first full frame.
func Dial(ctx context.Context, o Options) (*Client, error) {
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	addr := net.JoinHostPort(o.Host, strconv.Itoa(o.Port))
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", addr, err)
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(30 * time.Second)
	}
	c := &Client{conn: conn, r: bufio.NewReaderSize(conn, 1<<20), firstCh: make(chan struct{}), done: make(chan struct{})}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if err := c.handshake(o); err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	c.setPixelFormat()
	c.setEncodings(encZRLE, encCopyRect, encRaw, encDesktopSize, encLastRect)
	c.RequestUpdate(false)
	go c.readLoop()
	select {
	case <-c.firstCh:
		return c, nil
	case <-c.done:
		return nil, fmt.Errorf("connection closed before the first frame: %w", c.Err())
	case <-ctx.Done():
		c.Close()
		return nil, errors.New("timed out waiting for the first frame")
	}
}

func (c *Client) handshake(o Options) error {
	var ver [12]byte
	if _, err := io.ReadFull(c.r, ver[:]); err != nil {
		return fmt.Errorf("read RFB version: %w", err)
	}
	var major, minor int
	if _, err := fmt.Sscanf(string(ver[:]), "RFB %03d.%03d\n", &major, &minor); err != nil || major != 3 {
		return fmt.Errorf("not an RFB server (greeting %q)", ver[:])
	}
	c.Version = fmt.Sprintf("%d.%d", major, minor)
	proto := 8
	switch {
	case minor < 7:
		proto = 3
	case minor == 7:
		proto = 7
	}
	if _, err := c.conn.Write([]byte(fmt.Sprintf("RFB 003.%03d\n", proto))); err != nil {
		return err
	}

	var sec uint32
	if proto == 3 {
		if err := binary.Read(c.r, binary.BigEndian, &sec); err != nil {
			return err
		}
		if sec == 0 {
			return fmt.Errorf("server refused the connection: %s", c.readReason())
		}
	} else {
		n, err := c.r.ReadByte()
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("server refused the connection: %s", c.readReason())
		}
		types := make([]byte, n)
		if _, err := io.ReadFull(c.r, types); err != nil {
			return err
		}
		sec, err = chooseSecurity(types, o)
		if err != nil {
			return err
		}
		if _, err := c.conn.Write([]byte{byte(sec)}); err != nil {
			return err
		}
	}
	switch sec {
	case secNone:
	case secVNCAuth:
		if err := c.vncAuth(o.Password); err != nil {
			return err
		}
	case secARD:
		if err := c.ardAuth(o.Username, o.Password); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported security type %d", sec)
	}
	if proto == 8 || sec != secNone {
		var res uint32
		if err := binary.Read(c.r, binary.BigEndian, &res); err != nil {
			if errors.Is(err, io.EOF) {
				return &AuthError{}
			}
			return err
		}
		if res != 0 {
			reason := ""
			if proto == 8 {
				reason = c.readReason()
			}
			return &AuthError{Reason: reason}
		}
	}
	// ClientInit: shared, so other viewers (e.g. a person using Screen Sharing) stay connected.
	if _, err := c.conn.Write([]byte{1}); err != nil {
		return err
	}
	var si struct {
		W, H uint16
		PF   [16]byte
		NL   uint32
	}
	if err := binary.Read(c.r, binary.BigEndian, &si); err != nil {
		return fmt.Errorf("read ServerInit: %w", err)
	}
	if si.NL > 1<<16 {
		return errors.New("bad ServerInit")
	}
	name := make([]byte, si.NL)
	if _, err := io.ReadFull(c.r, name); err != nil {
		return err
	}
	c.Name = string(name)
	c.resize(int(si.W), int(si.H))
	return nil
}

func chooseSecurity(types []byte, o Options) (uint32, error) {
	has := func(t byte) bool {
		for _, x := range types {
			if x == t {
				return true
			}
		}
		return false
	}
	switch {
	case has(secARD) && o.Username != "":
		return secARD, nil
	case has(secVNCAuth):
		return secVNCAuth, nil
	case has(secNone):
		return secNone, nil
	case has(secARD):
		return 0, errors.New("the server requires a username (Apple authentication)")
	}
	return 0, fmt.Errorf("no supported VNC security type (server offers %v; supported: 30 Apple, 2 VNC password, 1 none)", types)
}

func (c *Client) readReason() string {
	var l uint32
	if binary.Read(c.r, binary.BigEndian, &l) != nil || l > 1<<16 {
		return "unknown reason"
	}
	b := make([]byte, l)
	if _, err := io.ReadFull(c.r, b); err != nil {
		return "unknown reason"
	}
	return string(b)
}

// ardAuth implements Apple Remote Desktop authentication (security type 30): Diffie-Hellman
// key agreement, MD5 of the shared secret as AES-128 key, credentials encrypted in ECB mode.
func (c *Client) ardAuth(user, pass string) error {
	if len(user) > 63 || len(pass) > 63 {
		return errors.New("username or password longer than 63 bytes is not supported by Apple authentication")
	}
	var hdr [4]byte
	if _, err := io.ReadFull(c.r, hdr[:]); err != nil {
		return err
	}
	g := new(big.Int).SetUint64(uint64(binary.BigEndian.Uint16(hdr[0:2])))
	keyLen := int(binary.BigEndian.Uint16(hdr[2:4]))
	if keyLen == 0 || keyLen > 1024 {
		return fmt.Errorf("bad ARD key length %d", keyLen)
	}
	buf := make([]byte, 2*keyLen)
	if _, err := io.ReadFull(c.r, buf); err != nil {
		return err
	}
	p := new(big.Int).SetBytes(buf[:keyLen])
	serverPub := new(big.Int).SetBytes(buf[keyLen:])
	privBytes := make([]byte, keyLen)
	if _, err := rand.Read(privBytes); err != nil {
		return err
	}
	priv := new(big.Int).SetBytes(privBytes)
	pub := new(big.Int).Exp(g, priv, p)
	shared := new(big.Int).Exp(serverPub, priv, p)
	key := md5.Sum(leftPad(shared.Bytes(), keyLen))

	creds := make([]byte, 128)
	if _, err := rand.Read(creds); err != nil {
		return err
	}
	copy(creds, user)
	creds[len(user)] = 0
	copy(creds[64:], pass)
	creds[64+len(pass)] = 0
	blk, err := aes.NewCipher(key[:])
	if err != nil {
		return err
	}
	out := make([]byte, 128, 128+keyLen)
	for i := 0; i < 128; i += aes.BlockSize {
		blk.Encrypt(out[i:i+aes.BlockSize], creds[i:i+aes.BlockSize])
	}
	_, err = c.conn.Write(append(out, leftPad(pub.Bytes(), keyLen)...))
	return err
}

// vncAuth implements classic VNC password authentication (DES with bit-reversed key bytes).
func (c *Client) vncAuth(pass string) error {
	var challenge [16]byte
	if _, err := io.ReadFull(c.r, challenge[:]); err != nil {
		return err
	}
	resp, err := vncAuthResponse(pass, challenge[:])
	if err != nil {
		return err
	}
	_, err = c.conn.Write(resp)
	return err
}

func vncAuthResponse(pass string, challenge []byte) ([]byte, error) {
	var key [8]byte
	copy(key[:], pass)
	for i, b := range key {
		var r byte
		for j := 0; j < 8; j++ {
			if b&(1<<j) != 0 {
				r |= 1 << (7 - j)
			}
		}
		key[i] = r
	}
	blk, err := des.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	out := make([]byte, 16)
	blk.Encrypt(out[:8], challenge[:8])
	blk.Encrypt(out[8:], challenge[8:16])
	return out, nil
}

func leftPad(b []byte, n int) []byte {
	if len(b) >= n {
		return b
	}
	return append(make([]byte, n-len(b)), b...)
}

// ---- client -> server ----

func (c *Client) write(b []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := c.conn.Write(b)
	if err != nil {
		c.fail(fmt.Errorf("write: %w", err))
	}
	return err
}

// 32 bpp, depth 24, little endian, true colour, R<<16|G<<8|B: B G R X in memory.
func (c *Client) setPixelFormat() {
	_ = c.write([]byte{0, 0, 0, 0, 32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0})
}

func (c *Client) setEncodings(encs ...int32) {
	b := []byte{2, 0, 0, 0}
	binary.BigEndian.PutUint16(b[2:], uint16(len(encs)))
	for _, e := range encs {
		b = binary.BigEndian.AppendUint32(b, uint32(e))
	}
	_ = c.write(b)
}

// RequestUpdate asks for a (possibly incremental) update of the whole screen.
func (c *Client) RequestUpdate(incremental bool) {
	c.mu.Lock()
	w, h := c.width, c.height
	c.mu.Unlock()
	b := []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if incremental {
		b[1] = 1
	}
	binary.BigEndian.PutUint16(b[6:], uint16(w))
	binary.BigEndian.PutUint16(b[8:], uint16(h))
	_ = c.write(b)
}

// Key sends a key event for an X11 keysym.
func (c *Client) Key(keysym uint32, down bool) error {
	b := []byte{4, 0, 0, 0, 0, 0, 0, 0}
	if down {
		b[1] = 1
	}
	binary.BigEndian.PutUint32(b[4:], keysym)
	return c.write(b)
}

// Pointer moves the pointer to (x,y) with the given button mask
// (bit 0 left, 1 middle, 2 right, 3/4 wheel up/down, 5/6 wheel left/right, 7 back).
func (c *Client) Pointer(buttons byte, x, y int) error {
	c.mu.Lock()
	x = clamp(x, 0, c.width-1)
	y = clamp(y, 0, c.height-1)
	c.buttons, c.ptrX, c.ptrY = buttons, x, y
	c.mu.Unlock()
	b := []byte{5, buttons, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(b[2:], uint16(x))
	binary.BigEndian.PutUint16(b[4:], uint16(y))
	return c.write(b)
}

// PointerState returns the current button mask and position.
func (c *Client) PointerState() (buttons byte, x, y int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buttons, c.ptrX, c.ptrY
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return max(lo, min(v, hi))
}

// ---- state ----

// Size returns the framebuffer size.
func (c *Client) Size() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.width, c.height
}

// Snapshot copies the framebuffer (BGRX, stride w*4).
func (c *Client) Snapshot() (w, h int, pix []byte, dirtySeq int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pix = make([]byte, len(c.fb))
	copy(pix, c.fb)
	return c.width, c.height, pix, c.dirtySeq
}

// Stats returns the change counter, time since the last change and since the first frame.
func (c *Client) Stats() (dirtySeq int, idle, sinceFirst time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.lastChange.IsZero() {
		idle = time.Since(c.lastChange)
	} else {
		idle = -1
	}
	if !c.firstFrame.IsZero() {
		sinceFirst = time.Since(c.firstFrame)
	}
	return c.dirtySeq, idle, sinceFirst
}

func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) Err() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.err
}

func (c *Client) fail(err error) {
	c.errMu.Lock()
	if c.err == nil {
		c.err = err
		close(c.done)
	}
	c.errMu.Unlock()
	_ = c.conn.Close()
}

// Close disconnects.
func (c *Client) Close() { c.fail(errors.New("closed")) }

func (c *Client) resize(w, h int) {
	c.mu.Lock()
	c.width, c.height = w, h
	c.fb = make([]byte, w*h*4)
	c.mu.Unlock()
}

// ---- server -> client ----

func (c *Client) readLoop() {
	for {
		t, err := c.r.ReadByte()
		if err != nil {
			c.fail(fmt.Errorf("connection lost: %w", err))
			return
		}
		switch t {
		case 0:
			err = c.readUpdate()
		case 1: // SetColourMapEntries (unused with true colour)
			var h struct {
				Pad      byte
				First, N uint16
			}
			if err = binary.Read(c.r, binary.BigEndian, &h); err == nil {
				_, err = io.CopyN(io.Discard, c.r, int64(h.N)*6)
			}
		case 2: // Bell
		case 3: // ServerCutText: not used (clipboard goes over SSH)
			var h struct {
				Pad [3]byte
				L   int32
			}
			if err = binary.Read(c.r, binary.BigEndian, &h); err == nil {
				n := int64(h.L)
				if n < 0 {
					n = -n
				}
				_, err = io.CopyN(io.Discard, c.r, n)
			}
		default:
			err = fmt.Errorf("unsupported server message type %d", t)
		}
		if err != nil {
			c.fail(err)
			return
		}
	}
}

func (c *Client) readUpdate() error {
	var h struct {
		Pad byte
		N   uint16
	}
	if err := binary.Read(c.r, binary.BigEndian, &h); err != nil {
		return err
	}
	changed := false
	for i := 0; i < int(h.N); i++ {
		var rh struct {
			X, Y, W, H uint16
			Enc        int32
		}
		if err := binary.Read(c.r, binary.BigEndian, &rh); err != nil {
			return err
		}
		x, y, w, hh := int(rh.X), int(rh.Y), int(rh.W), int(rh.H)
		switch rh.Enc {
		case encRaw:
			buf := make([]byte, w*hh*4)
			if _, err := io.ReadFull(c.r, buf); err != nil {
				return err
			}
			c.mu.Lock()
			c.blit(x, y, w, hh, buf)
			c.mu.Unlock()
			changed = true
		case encCopyRect:
			var s struct{ X, Y uint16 }
			if err := binary.Read(c.r, binary.BigEndian, &s); err != nil {
				return err
			}
			c.mu.Lock()
			c.copyRect(int(s.X), int(s.Y), x, y, w, hh)
			c.mu.Unlock()
			changed = true
		case encZRLE:
			var l uint32
			if err := binary.Read(c.r, binary.BigEndian, &l); err != nil {
				return err
			}
			if l > 64<<20 {
				return fmt.Errorf("ZRLE rectangle too large (%d bytes)", l)
			}
			data := make([]byte, l)
			if _, err := io.ReadFull(c.r, data); err != nil {
				return err
			}
			if err := c.zr.decode(data, x, y, w, hh, func(tx, ty, tw, th int, pix []byte) {
				c.mu.Lock()
				c.blit(tx, ty, tw, th, pix)
				c.mu.Unlock()
			}); err != nil {
				return fmt.Errorf("ZRLE: %w", err)
			}
			changed = true
		case encDesktopSize:
			c.resize(w, hh)
			if c.OnResize != nil {
				c.OnResize(w, hh)
			}
			changed = true
		case encLastRect:
			i = int(h.N)
		default:
			return fmt.Errorf("server sent unsupported encoding %d", rh.Enc)
		}
	}
	now := time.Now()
	c.mu.Lock()
	if changed {
		c.dirtySeq++
		c.lastChange = now
	}
	if c.firstFrame.IsZero() {
		c.firstFrame = now
	}
	c.mu.Unlock()
	c.firstOnce.Do(func() { close(c.firstCh) })
	c.RequestUpdate(true)
	if changed && c.OnUpdate != nil {
		c.OnUpdate()
	}
	return nil
}

// blit copies a w*h BGRX block into the framebuffer, clipped. Caller holds c.mu.
func (c *Client) blit(x, y, w, h int, pix []byte) {
	if x >= c.width || y >= c.height || x < 0 || y < 0 {
		return
	}
	n := min(w, c.width-x) * 4
	for row := 0; row < h && y+row < c.height; row++ {
		copy(c.fb[((y+row)*c.width+x)*4:], pix[row*w*4:row*w*4+n])
	}
}

// copyRect copies a framebuffer region (CopyRect encoding). Caller holds c.mu.
func (c *Client) copyRect(sx, sy, dx, dy, w, h int) {
	if sx+w > c.width || sy+h > c.height || dx+w > c.width || dy+h > c.height {
		return
	}
	tmp := make([]byte, w*h*4)
	for row := 0; row < h; row++ {
		copy(tmp[row*w*4:(row+1)*w*4], c.fb[((sy+row)*c.width+sx)*4:])
	}
	c.blit(dx, dy, w, h, tmp)
}
