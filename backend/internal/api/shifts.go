package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ---------- Shifts: when a person's day starts, and how late they may sign in ----------

// defaultShift applies to people without a shift: 9:30 with no grace period
var defaultShift = shiftTimes{Start: 9*60 + 30, Grace: 0}

type shiftTimes struct {
	Start int // minutes after midnight
	Grace int // minutes
}

// late reports whether a day starting inTime (seconds after midnight) is late for the shift.
// The whole grace minute still counts as on time: start 10:00 with 9 minutes grace is on time
// until 10:09:59, and late from 10:10.
func (t shiftTimes) late(inTime int) bool {
	return inTime >= (t.Start+t.Grace+1)*60
}

type shiftJSON struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Start        string    `json:"start"` // "HH:MM"
	GraceMinutes int       `json:"graceMinutes"`
	Members      int       `json:"members"` // active users on this shift
	CreatedAt    time.Time `json:"createdAt"`
}

func clock(minutes int) string {
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

// GET /api/shifts
func (s *Server) listShifts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `
		SELECT sh.id, sh.name, sh.start_minute, sh.grace_minutes, count(u.id), sh.created_at
		FROM shifts sh
		LEFT JOIN users u ON u.shift_id = sh.id AND u.status = 'active'
		GROUP BY sh.id
		ORDER BY sh.start_minute, lower(sh.name)`)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()

	shifts := []shiftJSON{}
	for rows.Next() {
		var sh shiftJSON
		var start int
		if err := rows.Scan(&sh.ID, &sh.Name, &start, &sh.GraceMinutes, &sh.Members, &sh.CreatedAt); err != nil {
			s.fail(w, r, err)
			return
		}
		sh.Start = clock(start)
		shifts = append(shifts, sh)
	}
	if err := rows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, shifts)
}

type shiftInput struct {
	Name         string `json:"name"`
	Start        string `json:"start"` // "HH:MM"
	GraceMinutes int    `json:"graceMinutes"`
	startMinute  int
}

// The same rules are in screen-management/src/app/pages/shifts/shifts.component.ts
func (in *shiftInput) valid(w http.ResponseWriter) bool {
	in.Name = strings.TrimSpace(in.Name)
	in.Start = strings.TrimSpace(in.Start)
	errs := map[string]string{}
	switch {
	case in.Name == "":
		errs["name"] = "Required"
	case len(in.Name) > 100:
		errs["name"] = "Use at most 100 characters"
	}
	if t, err := time.Parse("15:04", in.Start); err != nil || !clockPattern.MatchString(in.Start) {
		errs["start"] = "Enter a time as HH:MM"
	} else {
		in.startMinute = t.Hour()*60 + t.Minute()
	}
	if in.GraceMinutes < 0 || in.GraceMinutes > 240 {
		errs["graceMinutes"] = "Use 0 to 240 minutes"
	}
	if len(errs) > 0 {
		writeFieldErrors(w, errs)
		return false
	}
	return true
}

func (s *Server) shiftWriteError(w http.ResponseWriter, r *http.Request, err error) {
	if uniqueViolation(err) == "shifts_name_key" {
		writeJSON(w, http.StatusConflict, errorBody{Error: "Check the highlighted fields",
			Fields: map[string]string{"name": "A shift with this name already exists"}})
		return
	}
	s.fail(w, r, err)
}

func (s *Server) createShift(w http.ResponseWriter, r *http.Request) {
	var in shiftInput
	if !decode(w, r, &in) || !in.valid(w) {
		return
	}
	sh := shiftJSON{Name: in.Name, Start: clock(in.startMinute), GraceMinutes: in.GraceMinutes}
	err := s.db.QueryRow(r.Context(), `
		INSERT INTO shifts (name, start_minute, grace_minutes) VALUES ($1, $2, $3) RETURNING id, created_at`,
		in.Name, in.startMinute, in.GraceMinutes).Scan(&sh.ID, &sh.CreatedAt)
	if err != nil {
		s.shiftWriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, sh)
}

func (s *Server) updateShift(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in shiftInput
	if !decode(w, r, &in) || !in.valid(w) {
		return
	}
	tag, err := s.db.Exec(r.Context(), `
		UPDATE shifts SET name = $2, start_minute = $3, grace_minutes = $4 WHERE id = $1`,
		id, in.Name, in.startMinute, in.GraceMinutes)
	if err != nil {
		s.shiftWriteError(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "Shift not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/shifts/{id} — people on it move to the default shift
func (s *Server) deleteShift(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tag, err := s.db.Exec(r.Context(), `DELETE FROM shifts WHERE id = $1`, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "Shift not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
