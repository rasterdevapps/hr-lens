package api

import "testing"

func TestShiftLate(t *testing.T) {
	at := func(h, m, s int) int { return h*3600 + m*60 + s }
	tenWithGrace := shiftTimes{Start: 10 * 60, Grace: 9}
	cases := []struct {
		name   string
		shift  shiftTimes
		inTime int
		want   bool
	}{
		{"before start", tenWithGrace, at(9, 55, 0), false},
		{"at start", tenWithGrace, at(10, 0, 0), false},
		{"last grace minute", tenWithGrace, at(10, 9, 0), false},
		{"end of last grace minute", tenWithGrace, at(10, 9, 59), false},
		{"after grace", tenWithGrace, at(10, 10, 0), true},
		{"default on time", defaultShift, at(9, 30, 30), false},
		{"default late", defaultShift, at(9, 31, 0), true},
	}
	for _, c := range cases {
		if got := c.shift.late(c.inTime); got != c.want {
			t.Errorf("%s: late(%d) = %v, want %v", c.name, c.inTime, got, c.want)
		}
	}
}
