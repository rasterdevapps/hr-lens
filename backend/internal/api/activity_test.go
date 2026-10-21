package api

import (
	"net/http/httptest"
	"slices"
	"testing"
)

func TestParseActivityFilter(t *testing.T) {
	ok := []struct {
		query string
		users []int64
		tz    string
	}{
		{"from=2026-10-01&to=2026-10-01", []int64{}, "Asia/Kolkata"},
		{"from=2026-09-01&to=2026-09-30&users=2,5&tz=Etc/GMT%2B5&productivity=neutral", []int64{2, 5}, "Etc/GMT+5"},
	}
	for _, c := range ok {
		f, msg := parseActivityFilter(httptest.NewRequest("GET", "/x?"+c.query, nil))
		if msg != "" {
			t.Errorf("%s: unexpected error %q", c.query, msg)
			continue
		}
		if !slices.Equal(f.users, c.users) || f.tz != c.tz {
			t.Errorf("%s: got users %v tz %q", c.query, f.users, f.tz)
		}
	}

	bad := []string{
		"to=2026-10-01",                                    // no from
		"from=2026-10-02&to=2026-10-01",                    // backwards
		"from=2024-01-01&to=2026-10-01",                    // too long
		"from=2026-10-01&to=2026-10-01&tz=Nowhere/Land",    // unknown zone
		"from=2026-10-01&to=2026-10-01&productivity=great", // unknown level
		"from=2026-10-01&to=2026-10-01&users=2,abc",        // bad id
	}
	for _, q := range bad {
		if _, msg := parseActivityFilter(httptest.NewRequest("GET", "/x?"+q, nil)); msg == "" {
			t.Errorf("%s: expected an error", q)
		}
	}
}

func TestLikePattern(t *testing.T) {
	cases := map[string]string{"": "", "git": "%git%", "50%_off": `%50\%\_off%`, `a\b`: `%a\\b%`}
	for in, want := range cases {
		if got := likePattern(in); got != want {
			t.Errorf("likePattern(%q) = %q, want %q", in, got, want)
		}
	}
}
