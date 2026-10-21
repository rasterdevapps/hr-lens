//go:build integration

// Runs against the development database from .env (with seed data):
//
//	go test -tags integration ./internal/api -run Integration -v
//
// It changes one rule and puts it back.
package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"hrlens/backend/internal/auth"
	"hrlens/backend/internal/config"
	"hrlens/backend/internal/db"
)

func integrationServer(t *testing.T) *Server {
	t.Helper()
	// config reads .env from the working directory: the backend folder
	wd, _ := os.Getwd()
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	os.Chdir(wd)
	if err != nil {
		t.Skip("no configuration:", err)
	}
	pool, err := db.Open(context.Background(), cfg.DatabaseURL)
	if err != nil {
		t.Skip("no database:", err)
	}
	t.Cleanup(pool.Close)
	return New(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// call runs handler as the given user, decoding the JSON answer into out.
func call(t *testing.T, h http.HandlerFunc, me currentUser, method, target, body string, out any) int {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if id := strings.TrimPrefix(target, "/api/productivity/rules/"); id != target {
		req.SetPathValue("id", id)
	}
	req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, me))
	rec := httptest.NewRecorder()
	h(rec, req)
	if out != nil && rec.Code < 300 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: %v\n%s", method, target, err, rec.Body.String())
		}
	}
	if rec.Code >= 300 {
		t.Logf("%s %s -> %d %s", method, target, rec.Code, rec.Body.String())
	}
	return rec.Code
}

func TestIntegrationProductivity(t *testing.T) {
	s := integrationServer(t)
	var adminID int64
	if err := s.db.QueryRow(context.Background(), `SELECT id FROM users WHERE role = 'super_admin' ORDER BY id LIMIT 1`).Scan(&adminID); err != nil {
		t.Skip("no super admin:", err)
	}
	admin := currentUser{ID: adminID, Role: auth.SuperAdmin}

	// Rules list
	var list ruleList
	if code := call(t, s.listRules, admin, "GET", "/api/productivity/rules?kind=website&sort=time", "", &list); code != 200 {
		t.Fatalf("list websites: %d", code)
	}
	if list.Total == 0 || len(list.Items) == 0 || len(list.Categories) == 0 {
		t.Fatalf("expected seeded website rules, got %+v", list)
	}
	for i := 1; i < len(list.Items); i++ {
		if list.Items[i].Seconds > list.Items[i-1].Seconds {
			t.Fatalf("not sorted by time: %v then %v", list.Items[i-1], list.Items[i])
		}
	}
	t.Logf("websites: %d, top %s %ds %d hits (%s)", list.Total, list.Items[0].Name, list.Items[0].Seconds, list.Items[0].Hits, list.Items[0].Productivity)

	var apps ruleList
	call(t, s.listRules, admin, "GET", "/api/productivity/rules?kind=application&search=chrome", "", &apps)
	// "Browser" holds only while every Chrome row has a website: real agent data has none
	if len(apps.Items) != 1 {
		t.Fatalf("Chrome should be one rule, got %+v", apps.Items)
	}

	// Re-rate a website and see its activity follow, then put it back
	rule := list.Items[0]
	other := map[string]string{"productive": "unproductive", "unproductive": "productive", "neutral": "productive"}[rule.Productivity]
	countLevel := func(level string) (n int) {
		s.db.QueryRow(context.Background(), `SELECT count(*) FROM activity_events WHERE lower(domain) = $1 AND productivity::text = $2`, rule.Name, level).Scan(&n)
		return n
	}
	before := countLevel(rule.Productivity) + countLevel(other)
	if code := call(t, s.updateRule, admin, "PUT", "/api/productivity/rules/"+itoa(rule.ID),
		`{"category":"`+rule.Category+`","productivity":"`+other+`"}`, nil); code != 200 {
		t.Fatalf("update: %d", code)
	}
	if got := countLevel(other); got != before {
		t.Errorf("after re-rating, %d of %d events are %s", got, before, other)
	}
	if code := call(t, s.updateRules, admin, "PUT", "/api/productivity/rules",
		`{"ids":[`+itoa(rule.ID)+`],"productivity":"`+rule.Productivity+`"}`, nil); code != 200 {
		t.Fatalf("bulk update back: %d", code)
	}
	if got := countLevel(rule.Productivity); got != before {
		t.Errorf("after putting back, %d of %d events are %s", got, before, rule.Productivity)
	}

	// Bad input and duplicates
	if code := call(t, s.createRule, admin, "POST", "/api/productivity/rules",
		`{"kind":"website","name":"https://www.`+rule.Name+`/x","category":"News","productivity":"neutral"}`, nil); code != 422 {
		t.Errorf("duplicate website: got %d, want 422", code)
	}
	if code := call(t, s.createRule, admin, "POST", "/api/productivity/rules",
		`{"kind":"website","name":"not a site!","category":"News","productivity":"neutral"}`, nil); code != 422 {
		t.Errorf("bad website: got %d, want 422", code)
	}
	if code := call(t, s.updateRule, admin, "PUT", "/api/productivity/rules/999999999",
		`{"category":"News","productivity":"great"}`, nil); code != 422 {
		t.Errorf("bad level: got %d, want 422", code)
	}

	// Trends for one seeded manager on one weekday
	var mgr currentUser
	if err := s.db.QueryRow(context.Background(), `SELECT id FROM users WHERE email = 'priya.raman@example.com'`).Scan(&mgr.ID); err != nil {
		t.Skip("no seeded manager")
	}
	mgr.Role = auth.Manager
	day := time.Now().AddDate(0, 0, -1)
	for day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
		day = day.AddDate(0, 0, -1)
	}
	d := day.Format(time.DateOnly)
	var tr trendsJSON
	if code := call(t, s.productivityTrends, mgr, "GET", "/api/productivity/trends?from="+d+"&to="+d+"&users="+itoa(mgr.ID), "", &tr); code != 200 {
		t.Fatalf("trends: %d", code)
	}
	tt := tr.Totals
	if tt.Working == 0 || tt.Productive+tt.Neutral+tt.Unproductive != tt.Working {
		t.Errorf("levels do not add up to working time: %+v", tt)
	}
	if len(tr.Days) == 0 || tr.Days[0].Date != d || len(tr.Days[0].Segments) == 0 || len(tr.Breakdown) != 3 ||
		tr.Breakdown[0].Working != tt.Working || tr.Breakdown[0].Expected != expectedDaySeconds {
		t.Errorf("unexpected days/breakdown: %+v, %+v", tr.Days, tr.Breakdown)
	}

	// Productivity Bar: 5-minute slots that add up to the working time, none over capacity
	var barSum int64
	for _, b := range tr.Bars.Items {
		used := b.Productive + b.Neutral + b.Unproductive
		barSum += used
		if used > b.Capacity {
			t.Errorf("slot at %d over capacity: %+v", b.Start, b)
		}
	}
	if tr.Bars.Unit != "slot" || barSum != tt.Working {
		t.Errorf("bars: unit %q, %d slots adding to %d, want %d", tr.Bars.Unit, len(tr.Bars.Items), barSum, tt.Working)
	}

	// Categories add up to the working time, today's column matches the period
	var catSum int64
	for _, c := range tr.Categories.Period {
		catSum += c.Seconds
	}
	if catSum != tt.Working || len(tr.Categories.Breakdown) < len(tr.Categories.Period) {
		t.Errorf("categories add to %d, want %d: %+v", catSum, tt.Working, tr.Categories)
	}
	for i, c := range tr.Categories.Period {
		if got := tr.Categories.Breakdown[i].Cells[0].Seconds; got != c.Seconds {
			t.Errorf("category %s: today %d, period %d", c.Name, got, c.Seconds)
		}
	}

	// A week for one person: a timeline per day with activity, one bar per day
	monday := day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	var week trendsJSON
	call(t, s.productivityTrends, mgr, "GET", "/api/productivity/trends?from="+monday.Format(time.DateOnly)+"&to="+d+"&users="+itoa(mgr.ID), "", &week)
	if week.Bars.Unit != "day" || len(week.Bars.Items) != int(day.Sub(monday).Hours()/24)+1 || len(week.Days) == 0 || week.Days[0].Date != d {
		t.Errorf("week: bars %q x%d, %d days starting %v", week.Bars.Unit, len(week.Bars.Items), len(week.Days), week.Days)
	}
	t.Logf("trends %s: %+v, %d segments, %d slots, %d categories, week %+v", d, tt, len(tr.Days[0].Segments),
		len(tr.Bars.Items), len(tr.Categories.Period), tr.Breakdown[2])
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
