package vnc

// X11 keysyms for RDP scancodes (US layout). The service sends scancodes (internal/keymap);
// the VNC worker translates them here. On macOS Screen Sharing Super_L is the Command key and
// Alt_L is Option.

type scancode struct {
	code     uint8
	extended bool
}

var scanToKeysym = func() map[scancode]uint32 {
	m := map[scancode]uint32{
		{0x01, false}: 0xff1b, // Escape
		{0x0c, false}: '-', {0x0d, false}: '=', {0x0e, false}: 0xff08, {0x0f, false}: 0xff09,
		{0x1a, false}: '[', {0x1b, false}: ']', {0x1c, false}: 0xff0d, {0x1d, false}: 0xffe3,
		{0x27, false}: ';', {0x28, false}: '\'', {0x29, false}: '`', {0x2a, false}: 0xffe1,
		{0x2b, false}: '\\', {0x33, false}: ',', {0x34, false}: '.', {0x35, false}: '/',
		{0x36, false}: 0xffe2, {0x37, false}: 0xffaa, {0x38, false}: 0xffe9, {0x39, false}: ' ',
		{0x3a, false}: 0xffe5, {0x45, false}: 0xff7f, {0x46, false}: 0xff14,
		{0x47, false}: 0xffb7, {0x48, false}: 0xffb8, {0x49, false}: 0xffb9, {0x4a, false}: 0xffad,
		{0x4b, false}: 0xffb4, {0x4c, false}: 0xffb5, {0x4d, false}: 0xffb6, {0x4e, false}: 0xffab,
		{0x4f, false}: 0xffb1, {0x50, false}: 0xffb2, {0x51, false}: 0xffb3, {0x52, false}: 0xffb0,
		{0x53, false}: 0xffae, {0x57, false}: 0xffc8, {0x58, false}: 0xffc9, {0x76, false}: 0xffd5,
		// extended
		{0x1c, true}: 0xff8d, {0x1d, true}: 0xffe4, {0x35, true}: 0xffaf, {0x37, true}: 0xff61,
		{0x38, true}: 0xffea, {0x47, true}: 0xff50, {0x48, true}: 0xff52, {0x49, true}: 0xff55,
		{0x4b, true}: 0xff51, {0x4d, true}: 0xff53, {0x4f, true}: 0xff57, {0x50, true}: 0xff54,
		{0x51, true}: 0xff56, {0x52, true}: 0xff63, {0x53, true}: 0xffff,
		{0x5b, true}: 0xffeb, {0x5c, true}: 0xffec, {0x5d, true}: 0xff67,
	}
	for i, ch := range "1234567890" {
		m[scancode{uint8(0x02 + i), false}] = uint32(ch)
	}
	for row, letters := range map[uint8]string{0x10: "qwertyuiop", 0x1e: "asdfghjkl", 0x2c: "zxcvbnm"} {
		for i, ch := range letters {
			m[scancode{row + uint8(i), false}] = uint32(ch)
		}
	}
	for i := 0; i < 10; i++ { // F1..F10
		m[scancode{uint8(0x3b + i), false}] = 0xffbe + uint32(i)
	}
	for i := 0; i < 11; i++ { // F13..F23
		m[scancode{uint8(0x64 + i), false}] = 0xffca + uint32(i)
	}
	return m
}()

// KeysymForScancode returns the keysym for an RDP scancode.
func KeysymForScancode(code uint8, extended bool) (uint32, bool) {
	k, ok := scanToKeysym[scancode{code, extended}]
	return k, ok
}

// KeysymForRune returns the keysym that types r: Latin-1 characters map directly,
// others use the Unicode keysym range; newline and tab are Return and Tab.
func KeysymForRune(r rune) (uint32, bool) {
	switch {
	case r == '\n':
		return 0xff0d, true
	case r == '\t':
		return 0xff09, true
	case r == '\r' || r < 0x20 || r == 0x7f:
		return 0, false
	case r <= 0xff:
		return uint32(r), true
	case r <= 0x10ffff:
		return 0x01000000 | uint32(r), true
	}
	return 0, false
}
