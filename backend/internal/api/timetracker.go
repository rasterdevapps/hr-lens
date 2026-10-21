package api

import (
	"net/http"
	"sort"
)

// ---------- Time Tracker: one record per person per day, from activity ----------

// A pause longer than this between two activity rows counts as away (idle) time
const awayAfterSec = 120

type ttSegment struct {
	Start  int  `json:"start"`  // seconds after local midnight
	Length int  `json:"length"` // seconds
	Idle   bool `json:"idle"`   // away from the computer
}

type ttApp struct {
	Name    string `json:"name"`
	Seconds int    `json:"seconds"`
}

type ttDay struct {
	UserID        int64       `json:"userId"`
	Date          string      `json:"date"`     // YYYY-MM-DD in tz
	InTime        int         `json:"inTime"`   // first sign-in or unlock (else first activity), seconds after midnight
	SignedIn      bool        `json:"signedIn"` // InTime comes from a sign-in / unlock reported by the agent
	Finish        int         `json:"finish"`   // end of the last activity
	Working       int         `json:"working"`  // seconds of activity
	Productive    int         `json:"productive"`
	NonProductive int         `json:"nonProductive"`
	Neutral       int         `json:"neutral"`
	Idle          int         `json:"idle"`      // gaps longer than 2 minutes between first and last activity
	Late          bool        `json:"late"`      // started after the person's shift start + grace
	LateAfter     string      `json:"lateAfter"` // "HH:MM", the last on-time minute of their shift
	Apps          []ttApp     `json:"apps"`      // top 7 by time
	Segments      []ttSegment `json:"segments"`
}

// GET /api/timetracker/days?users=&from=&to=&tz= — days with activity only; a missing day was not worked.
// The start time is when the person first signed in to or unlocked their computer that day, as the
// agent reports it; without that, their first activity.
func (s *Server) timeTrackerDays(w http.ResponseWriter, r *http.Request) {
	f, msg := parseActivityFilter(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	f.productivity = ""
	me := userFrom(r)

	// First sign-in or unlock per person per day
	type personDay struct {
		user int64
		date string
	}
	signIn := map[personDay]int{}
	prows, err := s.db.Query(r.Context(), `
		SELECT p.user_id, to_char((p.at AT TIME ZONE $6)::date, 'YYYY-MM-DD'),
		       min(extract(epoch FROM (p.at AT TIME ZONE $6) - (p.at AT TIME ZONE $6)::date))::int
		FROM presence_events p
		WHERE p.kind IN ('login', 'unlock')
		  AND p.user_id IN (
			SELECT u.id FROM users u
			WHERE `+visiblePeople+` AND (cardinality($3::bigint[]) = 0 OR u.id = ANY($3::bigint[])))
		  AND p.at >= ($4::date)::timestamp AT TIME ZONE $6
		  AND p.at < ($5::date + 1)::timestamp AT TIME ZONE $6
		GROUP BY 1, 2`, f.args(me)[:6]...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for prows.Next() {
		var k personDay
		var at int
		if err := prows.Scan(&k.user, &k.date, &at); err != nil {
			prows.Close()
			s.fail(w, r, err)
			return
		}
		signIn[k] = at
	}
	prows.Close()
	if err := prows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}

	rows, err := s.db.Query(r.Context(), `
		SELECT e.user_id, to_char((e.started_at AT TIME ZONE $6)::date, 'YYYY-MM-DD'),
		       extract(epoch FROM (e.started_at AT TIME ZONE $6) - (e.started_at AT TIME ZONE $6)::date)::int,
		       e.duration_sec, e.productivity::text, e.app_name
		FROM activity_events e WHERE `+activityWhere+`
		ORDER BY e.user_id, e.started_at`, f.args(me)...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()

	out := []ttDay{}
	var day *ttDay
	var apps map[string]int
	var lastEnd int
	finishDay := func() {
		if day == nil {
			return
		}
		list := make([]ttApp, 0, len(apps))
		for name, secs := range apps {
			list = append(list, ttApp{name, secs})
		}
		sort.Slice(list, func(i, j int) bool {
			return list[i].Seconds > list[j].Seconds || (list[i].Seconds == list[j].Seconds && list[i].Name < list[j].Name)
		})
		if len(list) > 7 {
			list = list[:7]
		}
		day.Apps = list
		// Signed in before the first activity: the day starts there, and the wait counts as away
		if at, ok := signIn[personDay{day.UserID, day.Date}]; ok && at < day.InTime {
			day.Segments = append([]ttSegment{{Start: at, Length: day.InTime - at, Idle: true}}, day.Segments...)
			day.InTime = at
			day.SignedIn = true
		} else if ok {
			day.SignedIn = true
		}
		day.Idle = max(day.Finish-day.InTime-day.Working, 0)
		out = append(out, *day)
	}

	for rows.Next() {
		var userID int64
		var date, level, app string
		var start, secs int
		if err := rows.Scan(&userID, &date, &start, &secs, &level, &app); err != nil {
			s.fail(w, r, err)
			return
		}
		end := start + secs
		if day == nil || day.UserID != userID || day.Date != date {
			finishDay()
			day = &ttDay{UserID: userID, Date: date, InTime: start, Finish: end, Segments: []ttSegment{}}
			apps = map[string]int{}
			lastEnd = start
		}
		day.Finish = max(day.Finish, end)
		day.Working += secs
		switch level {
		case "productive":
			day.Productive += secs
		case "unproductive":
			day.NonProductive += secs
		default:
			day.Neutral += secs
		}
		apps[app] += secs

		// Timeline: working stretches, with a short pause bridged and a long one shown as idle
		segs := day.Segments
		gap := start - lastEnd
		switch {
		case len(segs) > 0 && gap <= awayAfterSec && !segs[len(segs)-1].Idle:
			segs[len(segs)-1].Length = max(segs[len(segs)-1].Length, end-segs[len(segs)-1].Start)
		default:
			if len(segs) > 0 && gap > awayAfterSec {
				segs = append(segs, ttSegment{Start: lastEnd, Length: gap, Idle: true})
			}
			segs = append(segs, ttSegment{Start: start, Length: secs})
		}
		day.Segments = segs
		lastEnd = max(lastEnd, end)
	}
	if err := rows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}
	finishDay()
	rows.Close()

	// Late against each person's shift, or the default shift for people without one
	ids := make([]int64, 0, len(out))
	for _, d := range out {
		ids = append(ids, d.UserID)
	}
	shifts := map[int64]shiftTimes{}
	srows, err := s.db.Query(r.Context(), `
		SELECT u.id, sh.start_minute, sh.grace_minutes
		FROM users u JOIN shifts sh ON sh.id = u.shift_id
		WHERE u.id = ANY($1::bigint[])`, ids)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer srows.Close()
	for srows.Next() {
		var id int64
		var t shiftTimes
		if err := srows.Scan(&id, &t.Start, &t.Grace); err != nil {
			s.fail(w, r, err)
			return
		}
		shifts[id] = t
	}
	if err := srows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}
	for i := range out {
		t, ok := shifts[out[i].UserID]
		if !ok {
			t = defaultShift
		}
		out[i].Late = t.late(out[i].InTime)
		out[i].LateAfter = clock((t.Start + t.Grace) % (24 * 60))
	}
	writeJSON(w, http.StatusOK, out)
}
