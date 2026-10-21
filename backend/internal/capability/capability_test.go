package capability

import "testing"

func TestLiveCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, os, report string
		want             bool
	}{
		{"legacy Linux", "linux/amd64", "", true},
		{"legacy empty OS", "", "", true},
		{"legacy Windows", "windows/amd64", "", false},
		{"unknown OS", "darwin/arm64", "", false},
		{"new Windows", "windows/amd64", `{"live_screen":true}`, true},
		{"disabled Linux", "linux/amd64", `{"live_screen":false}`, false},
		{"invalid report", "linux/amd64", `{"live_screen":"true"}`, false},
		{"empty report", "linux/amd64", `{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Live(tc.os, []byte(tc.report)); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
