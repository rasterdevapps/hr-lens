package main

import "testing"

// Pure message filters: tests never install a hook or receive real input.
func TestWindowsInputCountsRejectInjectedAndReleaseEvents(t *testing.T) {
	for _, msg := range []uintptr{0x100, 0x104} {
		if !keyCountable(msg, 0) {
			t.Fatal("physical key down rejected")
		}
		for _, flags := range []uint32{2, 16, 18} {
			if keyCountable(msg, flags) {
				t.Fatal("injected key counted")
			}
		}
	}
	for _, msg := range []uintptr{0x101, 0x105, 0x200} {
		if keyCountable(msg, 0) {
			t.Fatal("non-keydown counted")
		}
	}
	for _, msg := range []uintptr{0x201, 0x204, 0x207, 0x20b} {
		if !clickCountable(msg, 0) {
			t.Fatal("physical click rejected")
		}
		for _, flags := range []uint32{1, 2, 3} {
			if clickCountable(msg, flags) {
				t.Fatal("injected click counted")
			}
		}
	}
	for _, msg := range []uintptr{0x200, 0x202, 0x205, 0x208, 0x20c, 0x20a} {
		if clickCountable(msg, 0) {
			t.Fatal("move, scroll or release counted")
		}
	}
}
