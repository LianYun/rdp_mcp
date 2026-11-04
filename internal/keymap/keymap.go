// Package keymap maps key names to RDP scancodes and defines RDP mouse flags.
package keymap

import (
	"fmt"
	"strings"
)

// Key is an RDP keyboard scancode.
type Key struct {
	Code     uint8
	Extended bool
}

var keys = map[string]Key{
	"esc": {0x01, false}, "1": {0x02, false}, "2": {0x03, false}, "3": {0x04, false},
	"4": {0x05, false}, "5": {0x06, false}, "6": {0x07, false}, "7": {0x08, false},
	"8": {0x09, false}, "9": {0x0A, false}, "0": {0x0B, false}, "-": {0x0C, false},
	"=": {0x0D, false}, "backspace": {0x0E, false}, "tab": {0x0F, false},
	"q": {0x10, false}, "w": {0x11, false}, "e": {0x12, false}, "r": {0x13, false},
	"t": {0x14, false}, "y": {0x15, false}, "u": {0x16, false}, "i": {0x17, false},
	"o": {0x18, false}, "p": {0x19, false}, "[": {0x1A, false}, "]": {0x1B, false},
	"enter": {0x1C, false}, "ctrl": {0x1D, false},
	"a": {0x1E, false}, "s": {0x1F, false}, "d": {0x20, false}, "f": {0x21, false},
	"g": {0x22, false}, "h": {0x23, false}, "j": {0x24, false}, "k": {0x25, false},
	"l": {0x26, false}, ";": {0x27, false}, "'": {0x28, false}, "`": {0x29, false},
	"shift": {0x2A, false}, "\\": {0x2B, false},
	"z": {0x2C, false}, "x": {0x2D, false}, "c": {0x2E, false}, "v": {0x2F, false},
	"b": {0x30, false}, "n": {0x31, false}, "m": {0x32, false}, ",": {0x33, false},
	".": {0x34, false}, "/": {0x35, false}, "rshift": {0x36, false},
	"numpad*": {0x37, false}, "alt": {0x38, false}, "space": {0x39, false},
	"capslock": {0x3A, false},
	"f1":       {0x3B, false}, "f2": {0x3C, false}, "f3": {0x3D, false}, "f4": {0x3E, false},
	"f5": {0x3F, false}, "f6": {0x40, false}, "f7": {0x41, false}, "f8": {0x42, false},
	"f9": {0x43, false}, "f10": {0x44, false}, "numlock": {0x45, false},
	"scrolllock": {0x46, false},
	"numpad7":    {0x47, false}, "numpad8": {0x48, false}, "numpad9": {0x49, false},
	"numpad-": {0x4A, false}, "numpad4": {0x4B, false}, "numpad5": {0x4C, false},
	"numpad6": {0x4D, false}, "numpad+": {0x4E, false}, "numpad1": {0x4F, false},
	"numpad2": {0x50, false}, "numpad3": {0x51, false}, "numpad0": {0x52, false},
	"numpad.": {0x53, false},
	"f11":     {0x57, false}, "f12": {0x58, false},
	"f13": {0x64, false}, "f14": {0x65, false}, "f15": {0x66, false}, "f16": {0x67, false},
	"f17": {0x68, false}, "f18": {0x69, false}, "f19": {0x6A, false}, "f20": {0x6B, false},
	"f21": {0x6C, false}, "f22": {0x6D, false}, "f23": {0x6E, false}, "f24": {0x76, false},
	"numpadenter": {0x1C, true}, "rctrl": {0x1D, true}, "numpad/": {0x35, true},
	"printscreen": {0x37, true}, "ralt": {0x38, true},
	"home": {0x47, true}, "up": {0x48, true}, "pageup": {0x49, true},
	"left": {0x4B, true}, "right": {0x4D, true}, "end": {0x4F, true},
	"down": {0x50, true}, "pagedown": {0x51, true}, "insert": {0x52, true},
	"delete": {0x53, true}, "win": {0x5B, true}, "rwin": {0x5C, true},
	"menu": {0x5D, true},
}

var aliases = map[string]string{
	"control": "ctrl", "lctrl": "ctrl", "ctl": "ctrl",
	"lshift": "shift", "lalt": "alt", "option": "alt", "altgr": "ralt",
	"windows": "win", "lwin": "win", "super": "win", "meta": "win", "cmd": "win", "command": "win", "start": "win",
	"return": "enter", "escape": "esc", "del": "delete", "ins": "insert",
	"pgup": "pageup", "pgdn": "pagedown", "page_up": "pageup", "page_down": "pagedown",
	"arrowup": "up", "arrowdown": "down", "arrowleft": "left", "arrowright": "right",
	"bksp": "backspace", "back": "backspace", "spacebar": " ", "apps": "menu", "contextmenu": "menu",
	"prtsc": "printscreen", "print": "printscreen", "caps": "capslock",
	"minus": "-", "equal": "=", "equals": "=", "comma": ",", "period": ".", "dot": ".",
	"slash": "/", "backslash": "\\", "semicolon": ";", "quote": "'", "apostrophe": "'",
	"backquote": "`", "grave": "`", "bracketleft": "[", "bracketright": "]",
	"plus": "+",
}

// shifted symbols map to shift + base key.
var shifted = map[string]string{
	"!": "1", "@": "2", "#": "3", "$": "4", "%": "5", "^": "6", "&": "7", "*": "8",
	"(": "9", ")": "0", "_": "-", "+": "=", "{": "[", "}": "]", "|": "\\", ":": ";",
	"\"": "'", "~": "`", "<": ",", ">": ".", "?": "/",
}

// Lookup resolves a key name. needShift is true for shifted symbols like "!" or uppercase letters.
func Lookup(name string) (k Key, needShift bool, err error) {
	n := name
	if len(n) == 1 && n[0] >= 'A' && n[0] <= 'Z' {
		k = keys[strings.ToLower(n)]
		return k, true, nil
	}
	n = strings.ToLower(strings.TrimSpace(n))
	if name == " " {
		n = "space"
	}
	if a, ok := aliases[n]; ok {
		n = a
	}
	if n == " " {
		n = "space"
	}
	if k, ok := keys[n]; ok {
		return k, false, nil
	}
	if base, ok := shifted[n]; ok {
		return keys[base], true, nil
	}
	return Key{}, false, fmt.Errorf("unknown key %q", name)
}

// IsModifier reports whether the name refers to a modifier key.
func IsModifier(name string) bool {
	k, _, err := Lookup(name)
	if err != nil {
		return false
	}
	switch k {
	case keys["ctrl"], keys["rctrl"], keys["shift"], keys["rshift"], keys["alt"], keys["ralt"], keys["win"], keys["rwin"]:
		return true
	}
	return false
}

// FromDOMCode maps a browser KeyboardEvent.code to a scancode (used by the UI live view).
func FromDOMCode(code string) (Key, bool) {
	c := strings.ToLower(code)
	switch {
	case strings.HasPrefix(c, "key") && len(c) == 4:
		k, ok := keys[c[3:]]
		return k, ok
	case strings.HasPrefix(c, "digit") && len(c) == 6:
		k, ok := keys[c[5:]]
		return k, ok
	case strings.HasPrefix(c, "numpad") && len(c) == 7 && c[6] >= '0' && c[6] <= '9':
		k, ok := keys[c]
		return k, ok
	}
	dom := map[string]string{
		"controlleft": "ctrl", "controlright": "rctrl", "shiftleft": "shift", "shiftright": "rshift",
		"altleft": "alt", "altright": "ralt", "metaleft": "win", "metaright": "rwin",
		"osleft": "win", "osright": "rwin", "numpadadd": "numpad+", "numpadsubtract": "numpad-",
		"numpadmultiply": "numpad*", "numpaddivide": "numpad/", "numpaddecimal": "numpad.",
		"numpadenter": "numpadenter", "intlbackslash": "\\",
	}
	if n, ok := dom[c]; ok {
		k, ok := keys[n]
		return k, ok
	}
	k, _, err := Lookup(c)
	return k, err == nil
}

// RDP pointer flags (MS-RDPBCGR 2.2.8.1.1.3.1.1.3).
const (
	PtrHWheel        = 0x0400
	PtrWheel         = 0x0200
	PtrWheelNegative = 0x0100
	PtrMove          = 0x0800
	PtrDown          = 0x8000
	PtrButton1       = 0x1000 // left
	PtrButton2       = 0x2000 // right
	PtrButton3       = 0x4000 // middle
	PtrXDown         = 0x8000
	PtrXButton1      = 0x0001
	PtrXButton2      = 0x0002
	WheelDelta       = 120
)

// ButtonFlags returns the flags for a button and whether it is an extended (x1/x2) button.
func ButtonFlags(button string) (flags uint16, extended bool, err error) {
	switch strings.ToLower(button) {
	case "", "left":
		return PtrButton1, false, nil
	case "right":
		return PtrButton2, false, nil
	case "middle":
		return PtrButton3, false, nil
	case "x1", "back":
		return PtrXButton1, true, nil
	case "x2", "forward":
		return PtrXButton2, true, nil
	}
	return 0, false, fmt.Errorf("unknown button %q (left|right|middle|x1|x2)", button)
}

// WheelFlags encodes one wheel notch. vertical up = positive, horizontal right = positive.
func WheelFlags(horizontal, negative bool) uint16 {
	f := uint16(PtrWheel)
	if horizontal {
		f = PtrHWheel
	}
	if negative {
		return f | PtrWheelNegative | uint16((0x100-WheelDelta)&0xFF)
	}
	return f | WheelDelta
}
