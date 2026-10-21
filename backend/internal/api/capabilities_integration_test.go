//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hrlens/backend/internal/auth"
	"hrlens/backend/internal/capability"
)

func TestIntegrationDeviceCapabilities(t *testing.T) {
	s := integrationServer(t)
	ctx := context.Background()
	hash, err := auth.HashPassword("synthetic-test-password")
	if err != nil {
		t.Fatal(err)
	}
	var userID int64
	err = s.db.QueryRow(ctx, `INSERT INTO users (employee_code,first_name,last_name,email,password_hash) VALUES ('TEST-WINDOWS','Synthetic','Windows','windows-test@example.invalid',$1) RETURNING id`, hash).Scan(&userID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID) })
	body := `{"email":"windows-test@example.invalid","password":"synthetic-test-password","deviceName":"Synthetic desktop","os":"windows/amd64","agentVersion":"dev","capabilities":{"live_screen":true,"screenshots":true}}`
	rec := httptest.NewRecorder()
	s.registerDevice(rec, httptest.NewRequest("POST", "/api/agent/register", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	var deviceID int64
	var raw []byte
	if err := s.db.QueryRow(ctx, `SELECT id,capabilities FROM devices WHERE user_id=$1`, userID).Scan(&deviceID, &raw); err != nil {
		t.Fatal(err)
	}
	if !capability.Live("windows/amd64", raw) {
		t.Fatalf("registration lost report: %s", raw)
	}
	checkLive := func(want bool) {
		t.Helper()
		var states []liveStatus
		if code := call(t, s.liveStatus, currentUser{ID: userID, Role: auth.SuperAdmin}, "GET", "/api/monitoring/live?users="+itoa(userID), "", &states); code != 200 || len(states) != 1 {
			t.Fatalf("live status: %d %+v", code, states)
		}
		if states[0].NoScreen == want {
			t.Fatalf("capability gate: %+v", states[0])
		}
	}
	checkLive(true)
	for _, input := range []string{`{"capabilities":{"live_screen":false}}`, ""} {
		req := httptest.NewRequest("POST", "/api/agent/heartbeat", strings.NewReader(input))
		req = req.WithContext(context.WithValue(req.Context(), deviceKey{}, device{ID: deviceID, UserID: userID}))
		rec = httptest.NewRecorder()
		s.agentHeartbeat(rec, req)
		if rec.Code != 200 {
			t.Fatalf("heartbeat: %d %s", rec.Code, rec.Body.String())
		}
		checkLive(false)
	}
	// Old Windows stays unavailable; old Linux retains compatibility.
	if _, err := s.db.Exec(ctx, `UPDATE devices SET capabilities=NULL WHERE id=$1`, deviceID); err != nil {
		t.Fatal(err)
	}
	checkLive(false)
	if _, err := s.db.Exec(ctx, `UPDATE devices SET os='linux/amd64' WHERE id=$1`, deviceID); err != nil {
		t.Fatal(err)
	}
	checkLive(true)
	// Explicit false wins over OS even for Linux.
	caps, _ := json.Marshal(capability.Report{})
	if _, err := s.db.Exec(ctx, `UPDATE devices SET capabilities=$2 WHERE id=$1`, deviceID, caps); err != nil {
		t.Fatal(err)
	}
	checkLive(false)
}
