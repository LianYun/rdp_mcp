package keymap

import "testing"

func TestLookup(t *testing.T) {
	cases := []struct {
		name  string
		code  uint8
		ext   bool
		shift bool
	}{
		{"ctrl", 0x1D, false, false}, {"Control", 0x1D, false, false}, {"enter", 0x1C, false, false},
		{"win", 0x5B, true, false}, {"cmd", 0x5B, true, false}, {"delete", 0x53, true, false},
		{"a", 0x1E, false, false}, {"A", 0x1E, false, true}, {"!", 0x02, false, true},
		{"f12", 0x58, false, false}, {"up", 0x48, true, false}, {"space", 0x39, false, false}, {" ", 0x39, false, false},
	}
	for _, c := range cases {
		k, sh, err := Lookup(c.name)
		if err != nil || k.Code != c.code || k.Extended != c.ext || sh != c.shift {
			t.Errorf("%q: got %+v shift=%v err=%v", c.name, k, sh, err)
		}
	}
	if _, _, err := Lookup("nosuchkey"); err == nil {
		t.Error("expected error")
	}
}

func TestWheel(t *testing.T) {
	if WheelFlags(false, false) != PtrWheel|0x78 {
		t.Error("wheel up")
	}
	if WheelFlags(false, true) != PtrWheel|PtrWheelNegative|0x88 {
		t.Errorf("wheel down %#x", WheelFlags(false, true))
	}
}

func TestDOM(t *testing.T) {
	for code, want := range map[string]uint8{"KeyA": 0x1E, "Digit1": 0x02, "ControlLeft": 0x1D, "ArrowUp": 0x48, "Enter": 0x1C} {
		k, ok := FromDOMCode(code)
		if !ok || k.Code != want {
			t.Errorf("%s: %+v %v", code, k, ok)
		}
	}
}
