// vncprobe：验证 macOS 屏幕共享（VNC/RFB）+ SSH 作为 Remoter 目标的可行性。
//
// 一次性探针，不是产品代码：SSH 不校验主机密钥，错误处理从简。
//
//	go run ./tools/vncprobe -host 10.253.204.10 -user z.ai [-keychain remoter-probe] [-out /tmp/vncprobe] info|input|clip|ssh|all
//
// 密码从本机钥匙串读取（security find-generic-password -s <keychain> -a <user> -w）。
package main

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"
)

var (
	flagHost = flag.String("host", "", "Mac 地址")
	flagUser = flag.String("user", "", "Mac 账号")
	flagKC   = flag.String("keychain", "remoter-probe", "钥匙串服务名")
	flagOut  = flag.String("out", "/tmp/vncprobe", "输出目录（截图）")
)

const (
	encRaw         int32 = 0
	encCopyRect    int32 = 1
	encHextile     int32 = 5
	encZlib        int32 = 6
	encTight       int32 = 7
	encZRLE        int32 = 16
	encCursor      int32 = -239
	encPointerPos  int32 = -232
	encDesktopSize int32 = -223
	encLastRect    int32 = -224
	encExtDesktop  int32 = -308
	encExtClip     int32 = -1063131698 // 0xC0A1E5CE
)

func main() {
	flag.Parse()
	if *flagHost == "" || *flagUser == "" || flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "用法: vncprobe -host H -user U info|input|clip|ssh|all")
		os.Exit(2)
	}
	pass, err := keychainPassword(*flagKC, *flagUser)
	must(err)
	must(os.MkdirAll(*flagOut, 0o755))

	steps := map[string]func(string){
		"info": stepInfo, "input": stepInput, "input2": stepInput2, "clip": stepClip, "ssh": stepSSH,
	}
	if flag.Arg(0) == "all" {
		for _, s := range []string{"ssh", "info", "input", "clip"} {
			section(s)
			steps[s](pass)
		}
		return
	}
	f, ok := steps[flag.Arg(0)]
	if !ok {
		fmt.Fprintln(os.Stderr, "未知步骤", flag.Arg(0))
		os.Exit(2)
	}
	f(pass)
}

func section(s string) { fmt.Printf("\n==================== %s ====================\n", s) }

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func keychainPassword(service, account string) (string, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", service, "-a", account, "-w").Output()
	if err != nil {
		return "", fmt.Errorf("读取钥匙串 %s/%s: %w", service, account, err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// ---------------------------------------------------------------- RFB 客户端

type rfb struct {
	c        net.Conn
	r        *bufio.Reader
	version  string
	secTypes []byte
	name     string
	serverPF []byte

	mu                           sync.Mutex
	w, h                         int
	fb                           []byte // BGRX
	encSeen                      map[int32]int
	cursorW, cursorH, hotX, hotY int
	ptrX, ptrY                   int
	ptrSeen                      bool
	extClipCaps                  []byte

	upd  chan struct{}
	cut  chan []byte
	errc chan error
}

func dial(addr, user, pass string) (*rfb, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	v := &rfb{c: c, r: bufio.NewReaderSize(c, 1<<20), encSeen: map[int32]int{},
		upd: make(chan struct{}, 1), cut: make(chan []byte, 8), errc: make(chan error, 1)}
	c.SetDeadline(time.Now().Add(15 * time.Second))
	if err := v.handshake(user, pass); err != nil {
		c.Close()
		return nil, err
	}
	c.SetDeadline(time.Time{})
	go v.loop()
	return v, nil
}

func (v *rfb) handshake(user, pass string) error {
	ver := make([]byte, 12)
	if _, err := io.ReadFull(v.r, ver); err != nil {
		return err
	}
	v.version = strings.TrimSpace(string(ver))
	if _, err := v.c.Write([]byte("RFB 003.008\n")); err != nil {
		return err
	}
	n, err := v.r.ReadByte()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("服务端拒绝: %s", v.readReason())
	}
	v.secTypes = make([]byte, n)
	if _, err := io.ReadFull(v.r, v.secTypes); err != nil {
		return err
	}
	if bytes.IndexByte(v.secTypes, 30) < 0 {
		return fmt.Errorf("服务端不支持 ARD 认证(30)，可选 %v", v.secTypes)
	}
	v.c.Write([]byte{30})
	if err := v.ardAuth(user, pass); err != nil {
		return err
	}
	var res uint32
	if err := binary.Read(v.r, binary.BigEndian, &res); err != nil {
		return err
	}
	if res != 0 {
		return fmt.Errorf("认证失败(%d): %s", res, v.readReason())
	}
	v.c.Write([]byte{1}) // ClientInit shared=1
	var si struct {
		W, H uint16
		PF   [16]byte
		NL   uint32
	}
	if err := binary.Read(v.r, binary.BigEndian, &si); err != nil {
		return err
	}
	name := make([]byte, si.NL)
	io.ReadFull(v.r, name)
	v.name = string(name)
	v.serverPF = si.PF[:]
	v.resize(int(si.W), int(si.H))
	return nil
}

func (v *rfb) readReason() string {
	var l uint32
	if binary.Read(v.r, binary.BigEndian, &l) != nil || l > 1<<16 {
		return "?"
	}
	b := make([]byte, l)
	io.ReadFull(v.r, b)
	return string(b)
}

// Apple Remote Desktop 认证（security type 30）：DH 协商密钥，MD5(共享密钥) 作 AES-128-ECB 密钥加密账号密码。
func (v *rfb) ardAuth(user, pass string) error {
	if len(user) > 63 || len(pass) > 63 {
		return errors.New("账号或密码过长")
	}
	var hdr [4]byte
	if _, err := io.ReadFull(v.r, hdr[:]); err != nil {
		return err
	}
	g := new(big.Int).SetUint64(uint64(binary.BigEndian.Uint16(hdr[0:2])))
	keyLen := int(binary.BigEndian.Uint16(hdr[2:4]))
	buf := make([]byte, 2*keyLen)
	if _, err := io.ReadFull(v.r, buf); err != nil {
		return err
	}
	p := new(big.Int).SetBytes(buf[:keyLen])
	ys := new(big.Int).SetBytes(buf[keyLen:])
	privB := make([]byte, keyLen)
	rand.Read(privB)
	priv := new(big.Int).SetBytes(privB)
	pub := new(big.Int).Exp(g, priv, p)
	shared := new(big.Int).Exp(ys, priv, p)
	key := md5.Sum(leftPad(shared.Bytes(), keyLen))

	creds := make([]byte, 128)
	rand.Read(creds)
	copy(creds, user)
	creds[len(user)] = 0
	copy(creds[64:], pass)
	creds[64+len(pass)] = 0
	blk, _ := aes.NewCipher(key[:])
	enc := make([]byte, 128)
	for i := 0; i < 128; i += 16 {
		blk.Encrypt(enc[i:i+16], creds[i:i+16])
	}
	_, err := v.c.Write(append(enc, leftPad(pub.Bytes(), keyLen)...))
	return err
}

func leftPad(b []byte, n int) []byte {
	if len(b) >= n {
		return b
	}
	return append(make([]byte, n-len(b)), b...)
}

func (v *rfb) resize(w, h int) {
	v.mu.Lock()
	v.w, v.h = w, h
	v.fb = make([]byte, w*h*4)
	v.mu.Unlock()
}

func (v *rfb) close() { v.c.Close() }

// 32bpp、depth 24、小端、R<<16|G<<8|B → 内存中为 B G R X。
func (v *rfb) setPixelFormat() {
	pf := []byte{32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0}
	v.c.Write(append([]byte{0, 0, 0, 0}, pf...))
}

func (v *rfb) setEncodings(encs ...int32) {
	b := []byte{2, 0, 0, 0}
	binary.BigEndian.PutUint16(b[2:], uint16(len(encs)))
	for _, e := range encs {
		b = binary.BigEndian.AppendUint32(b, uint32(e))
	}
	v.c.Write(b)
}

func (v *rfb) requestUpdate(incremental bool) {
	v.mu.Lock()
	w, h := v.w, v.h
	v.mu.Unlock()
	inc := byte(0)
	if incremental {
		inc = 1
	}
	b := []byte{3, inc, 0, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(b[6:], uint16(w))
	binary.BigEndian.PutUint16(b[8:], uint16(h))
	v.c.Write(b)
}

// refresh 请求一次更新并等它到达；之后再等 quiet 时间收集后续更新。
func (v *rfb) refresh(incremental bool, timeout, quiet time.Duration) error {
	select {
	case <-v.upd:
	default:
	}
	v.requestUpdate(incremental)
	select {
	case <-v.upd:
	case err := <-v.errc:
		return err
	case <-time.After(timeout):
		return errors.New("等待画面更新超时")
	}
	end := time.After(quiet)
	for {
		v.requestUpdate(true)
		select {
		case <-v.upd:
		case err := <-v.errc:
			return err
		case <-end:
			return nil
		}
	}
}

func (v *rfb) key(sym uint32, down bool) {
	d := byte(0)
	if down {
		d = 1
	}
	b := []byte{4, d, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(b[4:], sym)
	v.c.Write(b)
}

// chord 依次按下、逆序松开。
func (v *rfb) chord(syms ...uint32) {
	for _, s := range syms {
		v.key(s, true)
		time.Sleep(30 * time.Millisecond)
	}
	for i := len(syms) - 1; i >= 0; i-- {
		v.key(syms[i], false)
		time.Sleep(30 * time.Millisecond)
	}
}

func (v *rfb) pointer(mask byte, x, y int) {
	b := []byte{5, mask, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(b[2:], uint16(x))
	binary.BigEndian.PutUint16(b[4:], uint16(y))
	v.c.Write(b)
}

func (v *rfb) clientCut(text []byte) {
	b := []byte{6, 0, 0, 0}
	b = binary.BigEndian.AppendUint32(b, uint32(len(text)))
	v.c.Write(append(b, text...))
}

func (v *rfb) loop() {
	fail := func(err error) {
		select {
		case v.errc <- err:
		default:
		}
	}
	for {
		t, err := v.r.ReadByte()
		if err != nil {
			fail(err)
			return
		}
		switch t {
		case 0:
			if err := v.readUpdate(); err != nil {
				fail(err)
				return
			}
			select {
			case v.upd <- struct{}{}:
			default:
			}
		case 1: // SetColourMapEntries
			var h struct {
				Pad      byte
				First, N uint16
			}
			binary.Read(v.r, binary.BigEndian, &h)
			io.CopyN(io.Discard, v.r, int64(h.N)*6)
		case 2: // Bell
		case 3: // ServerCutText
			var h struct {
				Pad [3]byte
				L   int32
			}
			if err := binary.Read(v.r, binary.BigEndian, &h); err != nil {
				fail(err)
				return
			}
			n := h.L
			if n < 0 {
				n = -n
			}
			b := make([]byte, n)
			if _, err := io.ReadFull(v.r, b); err != nil {
				fail(err)
				return
			}
			if h.L < 0 {
				v.mu.Lock()
				v.extClipCaps = b
				v.mu.Unlock()
				continue
			}
			select {
			case v.cut <- b:
			default:
			}
		default:
			fail(fmt.Errorf("未知服务端消息类型 %d", t))
			return
		}
	}
}

func (v *rfb) readUpdate() error {
	var h struct {
		Pad byte
		N   uint16
	}
	if err := binary.Read(v.r, binary.BigEndian, &h); err != nil {
		return err
	}
	for i := 0; i < int(h.N); i++ {
		var rh struct {
			X, Y, W, H uint16
			Enc        int32
		}
		if err := binary.Read(v.r, binary.BigEndian, &rh); err != nil {
			return err
		}
		x, y, w, hh := int(rh.X), int(rh.Y), int(rh.W), int(rh.H)
		v.mu.Lock()
		v.encSeen[rh.Enc]++
		v.mu.Unlock()
		switch rh.Enc {
		case encRaw:
			buf := make([]byte, w*hh*4)
			if _, err := io.ReadFull(v.r, buf); err != nil {
				return err
			}
			v.mu.Lock()
			for row := 0; row < hh && y+row < v.h; row++ {
				n := min(w, v.w-x) * 4
				if n > 0 {
					copy(v.fb[((y+row)*v.w+x)*4:], buf[row*w*4:row*w*4+n])
				}
			}
			v.mu.Unlock()
		case encCopyRect:
			var s struct{ X, Y uint16 }
			binary.Read(v.r, binary.BigEndian, &s)
			v.mu.Lock()
			tmp := make([]byte, w*hh*4)
			for row := 0; row < hh; row++ {
				copy(tmp[row*w*4:(row+1)*w*4], v.fb[((int(s.Y)+row)*v.w+int(s.X))*4:])
			}
			for row := 0; row < hh; row++ {
				copy(v.fb[((y+row)*v.w+x)*4:], tmp[row*w*4:(row+1)*w*4])
			}
			v.mu.Unlock()
		case encZlib, encZRLE: // 只为探测是否支持：按长度跳过，不解码
			var l uint32
			binary.Read(v.r, binary.BigEndian, &l)
			io.CopyN(io.Discard, v.r, int64(l))
		case encCursor:
			n := int64(w*hh*4 + (w+7)/8*hh)
			io.CopyN(io.Discard, v.r, n)
			v.mu.Lock()
			v.cursorW, v.cursorH, v.hotX, v.hotY = w, hh, x, y
			v.mu.Unlock()
		case encPointerPos:
			v.mu.Lock()
			v.ptrX, v.ptrY, v.ptrSeen = x, y, true
			v.mu.Unlock()
		case encDesktopSize:
			v.resize(w, hh)
		case encExtDesktop:
			var e struct {
				N   byte
				Pad [3]byte
			}
			binary.Read(v.r, binary.BigEndian, &e)
			io.CopyN(io.Discard, v.r, int64(e.N)*16)
			v.resize(w, hh)
		case encLastRect:
			return nil
		default:
			return fmt.Errorf("服务端使用了探针不解析的编码 %d（说明服务端支持它）", rh.Enc)
		}
	}
	return nil
}

func (v *rfb) savePNG(name string) string {
	v.mu.Lock()
	img := image.NewNRGBA(image.Rect(0, 0, v.w, v.h))
	for i := 0; i < v.w*v.h; i++ {
		img.Pix[i*4+0] = v.fb[i*4+2]
		img.Pix[i*4+1] = v.fb[i*4+1]
		img.Pix[i*4+2] = v.fb[i*4+0]
		img.Pix[i*4+3] = 255
	}
	v.mu.Unlock()
	p := filepath.Join(*flagOut, name)
	f, err := os.Create(p)
	must(err)
	png.Encode(f, img)
	f.Close()
	return p
}

func (v *rfb) encSummary() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	keys := make([]int, 0, len(v.encSeen))
	for k := range v.encSeen {
		keys = append(keys, int(k))
	}
	sort.Ints(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d×%d", k, v.encSeen[int32(k)]))
	}
	return strings.Join(parts, " ")
}

func vncAddr() string { return net.JoinHostPort(*flagHost, "5900") }

func mainConn(pass string) *rfb {
	v, err := dial(vncAddr(), *flagUser, pass)
	must(err)
	v.setPixelFormat()
	v.setEncodings(encCopyRect, encRaw, encCursor, encPointerPos, encDesktopSize, encExtDesktop, encLastRect, encExtClip)
	must(v.refresh(false, 20*time.Second, 800*time.Millisecond))
	return v
}

// ---------------------------------------------------------------- SSH

func sshDial(pass string) *ssh.Client {
	cfg := &ssh.ClientConfig{
		User: *flagUser,
		Auth: []ssh.AuthMethod{
			ssh.Password(pass),
			ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
				a := make([]string, len(qs))
				for i := range a {
					a[i] = pass
				}
				return a, nil
			}),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // 探针：不校验
		Timeout:         5 * time.Second,
	}
	c, err := ssh.Dial("tcp", net.JoinHostPort(*flagHost, "22"), cfg)
	must(err)
	return c
}

func sshRun(c *ssh.Client, cmd string) (string, error) {
	s, err := c.NewSession()
	if err != nil {
		return "", err
	}
	defer s.Close()
	out, err := s.CombinedOutput(cmd)
	return strings.TrimRight(string(out), "\n"), err
}

func show(c *ssh.Client, label, cmd string) string {
	out, err := sshRun(c, cmd)
	fmt.Printf("--- %s\n$ %s\n%s\n", label, cmd, out)
	if err != nil {
		fmt.Printf("(退出: %v)\n", err)
	}
	return out
}

const jxaDisplay = `osascript -l JavaScript -e 'ObjC.import("AppKit"); var s=$.NSScreen.mainScreen; var f=s.frame.size; var m=$.NSEvent.mouseLocation; "screen_points="+f.width+"x"+f.height+" scale="+s.backingScaleFactor+" mouse(points,左下原点)="+m.x+","+m.y'`

// ---------------------------------------------------------------- 步骤

func stepSSH(pass string) {
	c := sshDial(pass)
	defer c.Close()
	fmt.Println("SSH 认证成功")
	show(c, "系统", "sw_vers; uname -m; id")
	show(c, "控制台用户（GUI 登录者）", "stat -f %Su /dev/console")
	show(c, "显示器", "system_profiler SPDisplaysDataType 2>/dev/null | sed -n '/Displays:/,$p'")
	show(c, "屏幕尺寸/缩放/鼠标位置（经 SSH 调 AppKit）", jxaDisplay)
	show(c, "锁屏状态", `ioreg -n Root -d1 -a 2>/dev/null | plutil -extract IOConsoleUsers xml1 -o - - 2>/dev/null | grep -A1 -E 'CGSSessionScreenIsLocked|kCGSSessionOnConsoleKey' | tr -d '\t'`)
	show(c, "FileVault", "fdesetup status")
	show(c, "睡眠设置", "pmset -g | grep -E ' sleep|displaysleep|disksleep|womp|autorestart'")
	show(c, "屏幕共享/远程管理服务", "launchctl print system/com.apple.screensharing 2>/dev/null | grep -E 'state|pid' | head -3; pgrep -lf ARDAgent")
	show(c, "中文输出（UTF-8 回传）", "printf '中文 é ✓\\n'; locale | head -2")

	s, err := c.NewSession()
	if err == nil {
		err = s.RequestSubsystem("sftp")
		s.Close()
	}
	fmt.Printf("--- SFTP 子系统: %v\n", map[bool]string{true: "可用", false: fmt.Sprint("不可用: ", err)}[err == nil])

	show(c, "SSH 里截屏（检验 TCC 屏幕录制权限）", "rm -f /tmp/remoter-sc.png; screencapture -x /tmp/remoter-sc.png; echo exit=$?; sips -g pixelWidth -g pixelHeight /tmp/remoter-sc.png 2>&1 | tail -2")
}

func stepInfo(pass string) {
	v := mainConn(pass)
	defer v.close()
	fmt.Printf("服务端版本: %s\n安全类型: %v\n桌面名: %q\n帧缓冲: %dx%d\n服务端像素格式: % x\n", v.version, v.secTypes, v.name, v.w, v.h, v.serverPF)
	fmt.Printf("首次全屏更新用到的编码(编码×矩形数): %s\n", v.encSummary())
	v.mu.Lock()
	fmt.Printf("光标伪编码: %v（%dx%d 热点 %d,%d）\n", v.cursorW > 0, v.cursorW, v.cursorH, v.hotX, v.hotY)
	fmt.Printf("指针位置伪编码: %v (%d,%d)\n", v.ptrSeen, v.ptrX, v.ptrY)
	fmt.Printf("扩展剪贴板能力: %v\n", v.extClipCaps != nil)
	v.mu.Unlock()
	fmt.Println("截图:", v.savePNG("info.png"))

	// 增量更新：空闲屏幕上是否只推送变化
	t0 := time.Now()
	err := v.refresh(true, 3*time.Second, 0)
	fmt.Printf("空闲时增量更新: %v，用时 %v，累计编码 %s\n", errOr(err, "收到"), time.Since(t0).Round(time.Millisecond), v.encSummary())

	// 全屏 raw 拉取耗时
	t0 = time.Now()
	must(v.refresh(false, 20*time.Second, 0))
	fmt.Printf("全屏非增量 Raw 拉取用时 %v（%.1f MB）\n", time.Since(t0).Round(time.Millisecond), float64(v.w*v.h*4)/1e6)

	for _, e := range []int32{encZRLE, encZlib, encTight, encHextile} {
		fmt.Printf("编码 %d 支持探测: %s\n", e, probeEncoding(pass, e))
	}
}

func errOr(err error, ok string) string {
	if err != nil {
		return err.Error()
	}
	return ok
}

func probeEncoding(pass string, enc int32) string {
	v, err := dial(vncAddr(), *flagUser, pass)
	if err != nil {
		return "连接失败: " + err.Error()
	}
	defer v.close()
	v.setPixelFormat()
	v.setEncodings(enc, encRaw)
	if err := v.refresh(false, 20*time.Second, 0); err != nil {
		return err.Error()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.encSeen[enc] > 0 {
		return fmt.Sprintf("支持（%s）", fmtSeen(v.encSeen))
	}
	return fmt.Sprintf("未使用，回退为 %s", fmtSeen(v.encSeen))
}

func fmtSeen(m map[int32]int) string {
	var s []string
	for k, n := range m {
		s = append(s, fmt.Sprintf("%d×%d", k, n))
	}
	sort.Strings(s)
	return strings.Join(s, " ")
}

const probeFile = "/tmp/remoter-probe.txt"

func stepInput(pass string) {
	c := sshDial(pass)
	defer c.Close()
	show(c, "准备 TextEdit", fmt.Sprintf("killall TextEdit 2>/dev/null; sleep 1; printf 'start\\n' > %s; open -a TextEdit %s; sleep 2; echo ok", probeFile, probeFile))

	v := mainConn(pass)
	defer v.close()
	fmt.Printf("帧缓冲 %dx%d\n", v.w, v.h)

	// 鼠标：移动到帧缓冲坐标，经 AppKit 读回（points，左下原点）
	for _, pt := range [][2]int{{200, 150}, {v.w / 2, v.h / 2}} {
		v.pointer(0, pt[0], pt[1])
		time.Sleep(400 * time.Millisecond)
		out, _ := sshRun(c, jxaDisplay)
		fmt.Printf("VNC 指针移到帧缓冲 (%d,%d) → %s\n", pt[0], pt[1], out)
	}
	v.mu.Lock()
	fmt.Printf("服务端回报指针位置: %v (%d,%d)\n", v.ptrSeen, v.ptrX, v.ptrY)
	v.mu.Unlock()

	// 键盘：ASCII 直接发 keysym（大写不加 Shift），再一行大写手动加 Shift，再 Unicode keysym
	typeRaw := func(s string) {
		for _, r := range s {
			sym := uint32(r)
			if r == '\n' {
				sym = 0xff0d
			} else if r > 0xff {
				sym = 0x01000000 | uint32(r)
			}
			v.chord(sym)
		}
	}
	const shiftL = 0xffe1
	v.chord(0xff57) // End（TextEdit 中光标可能在开头）
	typeRaw("\nL1 raw: abc XYZ 019 !@#$%^&*()_+-=[]{};:'\",.<>/?|\\`~\n")
	typeRaw("L2 shift: ")
	for _, r := range "XYZ!@#" {
		v.chord(shiftL, uint32(r))
	}
	typeRaw("\nL3 latin1: é ü ß\n")
	typeRaw("L4 unicode: 中文测试 ✓ ")
	typeRaw("\n")
	time.Sleep(500 * time.Millisecond)
	must(v.refresh(true, 5*time.Second, 500*time.Millisecond))
	fmt.Println("输入后截图:", v.savePNG("input-typed.png"))

	// Command 键对应哪个 keysym：逐个试 X+s 保存，看文件是否变化
	candidates := []struct {
		name string
		sym  uint32
	}{{"Super_L", 0xffeb}, {"Meta_L", 0xffe7}, {"Alt_L", 0xffe9}}
	for _, cand := range candidates {
		v.chord(cand.sym, 's')
		time.Sleep(1500 * time.Millisecond)
		out, _ := sshRun(c, "cat "+probeFile)
		saved := out != "start"
		fmt.Printf("%s+s 后文件是否被保存: %v\n", cand.name, saved)
		if saved {
			fmt.Printf("=> Command = %s (0x%x)\n文件内容:\n%s\n", cand.name, cand.sym, out)
			fmt.Printf("hex(L4 行): % x\n", lineWith(out, "L4"))
			break
		}
	}
	must(v.refresh(true, 5*time.Second, 500*time.Millisecond))
	fmt.Println("保存后截图:", v.savePNG("input-saved.png"))

	// 滚轮：按钮 4/5
	v.pointer(0, v.w/2, v.h/2)
	for i := 0; i < 3; i++ {
		v.pointer(1<<3, v.w/2, v.h/2)
		v.pointer(0, v.w/2, v.h/2)
	}
	fmt.Println("滚轮事件已发送（向上 3 格）")

	sshRun(c, "killall TextEdit 2>/dev/null")
}

// stepInput2：绕过输入法的两种打字方式——ASCII 也用 Unicode keysym；SSH pbcopy + Cmd+V 粘贴。
func stepInput2(pass string) {
	c := sshDial(pass)
	defer c.Close()
	show(c, "当前输入源", "defaults read com.apple.HIToolbox AppleSelectedInputSources 2>/dev/null | grep -E 'Input Mode|KeyboardLayout Name|Bundle ID'")
	show(c, "准备 TextEdit", fmt.Sprintf("killall TextEdit 2>/dev/null; sleep 1; : > %s; open -a TextEdit %s; sleep 2; echo ok", probeFile, probeFile))

	v := mainConn(pass)
	defer v.close()
	const super = 0xffeb
	for _, r := range "A uni: abc XYZ 019 !@#$%^&*() .,:;'\"?/\\|`~ é 中文" {
		v.chord(0x01000000 | uint32(r))
	}
	v.chord(0xff0d)
	show(c, "SSH pbcopy（UTF-8）", `printf 'B paste: abc XYZ !@# .,:;"? 中文 ✓' | LANG=en_US.UTF-8 pbcopy; LANG=en_US.UTF-8 pbpaste`)
	v.chord(super, 'v')
	v.chord(0xff0d)
	time.Sleep(300 * time.Millisecond)
	v.chord(super, 's')
	time.Sleep(1500 * time.Millisecond)
	show(c, "文件内容", "cat "+probeFile)
	must(v.refresh(true, 5*time.Second, 500*time.Millisecond))
	fmt.Println("截图:", v.savePNG("input2.png"))
	sshRun(c, "killall TextEdit 2>/dev/null")
}

func lineWith(s, prefix string) []byte {
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, prefix) {
			return []byte(l)
		}
	}
	return nil
}

func stepClip(pass string) {
	c := sshDial(pass)
	defer c.Close()
	v := mainConn(pass)
	defer v.close()

	drain := func() {
		for {
			select {
			case <-v.cut:
			default:
				return
			}
		}
	}
	waitCut := func(d time.Duration) []byte {
		end := time.After(d)
		for {
			v.requestUpdate(true) // 有些服务端只在处理消息时顺带推剪贴板
			select {
			case b := <-v.cut:
				return b
			case <-end:
				return nil
			case <-time.After(200 * time.Millisecond):
			}
		}
	}

	// Mac → 客户端
	for _, text := range []string{"from-mac ascii", "from-mac café", "from-mac 中文"} {
		drain()
		show(c, "Mac 端 pbcopy", fmt.Sprintf("printf '%%s' '%s' | pbcopy; pbpaste", text))
		b := waitCut(3 * time.Second)
		if b == nil {
			fmt.Println("  客户端未收到 ServerCutText")
			continue
		}
		fmt.Printf("  收到 ServerCutText: % x\n  按 UTF-8: %q（合法 UTF-8: %v）  按 Latin-1: %q\n", b, string(b), utf8.Valid(b), latin1(b))
	}

	// 客户端 → Mac
	for _, tc := range []struct {
		label string
		data  []byte
	}{
		{"ASCII", []byte("from-probe ascii")},
		{"Latin-1 编码的 café", []byte{'c', 'a', 'f', 0xe9}},
		{"UTF-8 编码的 café 中文", []byte("café 中文")},
	} {
		v.clientCut(tc.data)
		time.Sleep(800 * time.Millisecond)
		out, _ := sshRun(c, "pbpaste | od -An -tx1 | tr -s ' '; pbpaste")
		fmt.Printf("ClientCutText %s (% x) → Mac pbpaste:\n%s\n", tc.label, tc.data, out)
	}
}

func latin1(b []byte) string {
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}
