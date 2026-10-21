package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // time zone names work even where the OS has no zone files
)

// ---------- Filters shared by every activity request ----------

// activityFilter is read from the query string:
//
//	users=1,2,3       people to include (default: everyone the caller may see)
//	from, to          YYYY-MM-DD, inclusive, as calendar days in tz
//	tz                IANA zone name, default Asia/Kolkata
//	productivity      productive | neutral | unproductive (default: all)
//	search            text to look for
type activityFilter struct {
	users        []int64
	from, to     time.Time
	tz           string
	productivity string
	search       string
}

const maxActivityDays = 366

func parseActivityFilter(r *http.Request) (activityFilter, string) {
	q := r.URL.Query()
	f := activityFilter{
		tz:           cmp(q.Get("tz"), "Asia/Kolkata"),
		productivity: q.Get("productivity"),
		search:       strings.TrimSpace(q.Get("search")),
		users:        []int64{},
	}
	if _, err := time.LoadLocation(f.tz); err != nil {
		return f, "tz must be a time zone name such as Asia/Kolkata"
	}
	var err error
	if f.from, err = time.Parse(time.DateOnly, q.Get("from")); err != nil {
		return f, "from must be a date (YYYY-MM-DD)"
	}
	if f.to, err = time.Parse(time.DateOnly, q.Get("to")); err != nil {
		return f, "to must be a date (YYYY-MM-DD)"
	}
	if f.to.Before(f.from) {
		return f, "to must not be before from"
	}
	if f.to.Sub(f.from) > maxActivityDays*24*time.Hour {
		return f, fmt.Sprintf("choose at most %d days", maxActivityDays)
	}
	switch f.productivity {
	case "", "productive", "neutral", "unproductive":
	default:
		return f, "productivity must be productive, neutral or unproductive"
	}
	if raw := q.Get("users"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
			if err != nil || id <= 0 {
				return f, "users must be a list of user ids"
			}
			f.users = append(f.users, id)
		}
	}
	if len(f.search) > 200 {
		f.search = f.search[:200]
	}
	return f, ""
}

// likePattern makes a case-insensitive "contains" pattern, treating % and _ literally.
func likePattern(s string) string {
	if s == "" {
		return ""
	}
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

// activityWhere limits activity_events (as "e") to the filter and to people the caller may see.
// Parameters: $1 role, $2 caller id, $3 user ids, $4 from, $5 to, $6 tz ($7 productivity in activityWhere).
const activityPeriod = `
	e.user_id IN (
		SELECT u.id FROM users u
		WHERE ` + visiblePeople + ` AND (cardinality($3::bigint[]) = 0 OR u.id = ANY($3::bigint[])))
	AND e.started_at >= ($4::date)::timestamp AT TIME ZONE $6
	AND e.started_at < ($5::date + 1)::timestamp AT TIME ZONE $6`

const activityWhere = activityPeriod + ` AND ($7 = '' OR e.productivity::text = $7)`

func (f activityFilter) args(me currentUser) []any {
	return append(f.periodArgs(me), f.productivity)
}

// periodArgs are the parameters of activityPeriod alone ($1–$6), without the productivity level.
func (f activityFilter) periodArgs(me currentUser) []any {
	return []any{string(me.Role), me.ID, f.users, f.from.Format(time.DateOnly), f.to.Format(time.DateOnly), f.tz}
}

// ---------- Summary: websites, applications, categories ----------

type activityItem struct {
	Name         string  `json:"name"`
	Detail       string  `json:"detail"` // browser for a website, process for an app, app count for a category
	Category     string  `json:"category"`
	Productivity string  `json:"productivity"`
	Seconds      int64   `json:"seconds"`
	Percent      float64 `json:"percent"` // share of all time in this view, ignoring the search
}

type activityTotals struct {
	Seconds      int64 `json:"seconds"`
	Productive   int64 `json:"productive"`
	Neutral      int64 `json:"neutral"`
	Unproductive int64 `json:"unproductive"`
}

type activitySummary struct {
	Totals activityTotals `json:"totals"`
	Items  []activityItem `json:"items"`
}

// What to group by, and what to show beside each name
var activityGroups = map[string]struct{ key, detail string }{
	"website":     {"e.domain", "mode() WITHIN GROUP (ORDER BY e.app_name)"},
	"application": {"e.app_name", "mode() WITHIN GROUP (ORDER BY e.app)"},
	"category":    {"e.category", "count(DISTINCT e.app_name)::text"},
}

const (
	maxActivityItems = 500  // rows returned
	maxActivityScan  = 5000 // rows searched
)

// GET /api/activity/summary?group=website|application|category&<filters>
func (s *Server) activitySummary(w http.ResponseWriter, r *http.Request) {
	group, ok := activityGroups[r.URL.Query().Get("group")]
	if !ok {
		writeError(w, http.StatusBadRequest, "group must be website, application or category")
		return
	}
	f, msg := parseActivityFilter(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	me := userFrom(r)
	// The level filter picks rows; the totals strip and each row's share still describe the whole period
	where := activityPeriod + ` AND ` + group.key + ` IS NOT NULL`
	out := activitySummary{Items: []activityItem{}}

	// Totals per productivity level, for the summary strip
	rows, err := s.db.Query(r.Context(), `
		SELECT e.productivity::text, sum(e.duration_sec)
		FROM activity_events e WHERE `+where+`
		GROUP BY 1`, f.periodArgs(me)...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for rows.Next() {
		var level string
		var secs int64
		if err := rows.Scan(&level, &secs); err != nil {
			rows.Close()
			s.fail(w, r, err)
			return
		}
		out.Totals.Seconds += secs
		switch level {
		case "productive":
			out.Totals.Productive = secs
		case "neutral":
			out.Totals.Neutral = secs
		case "unproductive":
			out.Totals.Unproductive = secs
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}

	// One row per website / application / category, largest first. A browser mixes
	// productive and unproductive sites, so each row takes the level and category
	// it spent the most time in.
	rows, err = s.db.Query(r.Context(), `
		WITH e AS (
			SELECT `+group.key+` AS name, e.* FROM activity_events e WHERE `+where+`
		), g AS (
			SELECT name, `+group.detail+` AS detail, sum(e.duration_sec) AS secs FROM e GROUP BY name
		), level AS (
			SELECT DISTINCT ON (name) name, productivity::text AS productivity
			FROM (SELECT name, productivity, sum(duration_sec) AS secs FROM e GROUP BY 1, 2) x
			ORDER BY name, secs DESC, productivity
		), cat AS (
			SELECT DISTINCT ON (name) name, category
			FROM (SELECT name, category, sum(duration_sec) AS secs FROM e GROUP BY 1, 2) x
			ORDER BY name, secs DESC, category
		)
		SELECT g.name, g.detail, cat.category, level.productivity, g.secs,
		       round(100.0 * g.secs / NULLIF((SELECT sum(secs) FROM g), 0), 2)::float8
		FROM g JOIN level USING (name) JOIN cat USING (name)
		WHERE $7 = '' OR level.productivity = $7
		ORDER BY g.secs DESC, g.name
		LIMIT $8`, append(f.args(me), maxActivityScan)...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()
	pattern := strings.ToLower(f.search)
	for rows.Next() {
		var it activityItem
		if err := rows.Scan(&it.Name, &it.Detail, &it.Category, &it.Productivity, &it.Seconds, &it.Percent); err != nil {
			s.fail(w, r, err)
			return
		}
		// Percentages are worked out over everything; the search only hides rows
		if pattern != "" && !strings.Contains(strings.ToLower(it.Name), pattern) {
			continue
		}
		if len(out.Items) < maxActivityItems {
			out.Items = append(out.Items, it)
		}
	}
	if err := rows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- Detailed activity ----------

type activityEvent struct {
	ID           int64     `json:"id"`
	UserID       int64     `json:"userId"`
	UserName     string    `json:"userName"`
	StartedAt    time.Time `json:"startedAt"`
	Seconds      int       `json:"seconds"`
	App          string    `json:"app"`
	AppName      string    `json:"appName"`
	Title        string    `json:"title"`
	Domain       *string   `json:"domain"`
	Category     string    `json:"category"`
	Productivity string    `json:"productivity"`
}

type activityEvents struct {
	Total int             `json:"total"`
	Items []activityEvent `json:"items"`
}

// GET /api/activity/events?<filters>&page=0&pageSize=50&dir=desc
func (s *Server) activityEvents(w http.ResponseWriter, r *http.Request) {
	f, msg := parseActivityFilter(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("pageSize"))
	if page < 0 {
		page = 0
	}
	if size <= 0 || size > 200 {
		size = 50
	}
	dir := "DESC"
	if q.Get("dir") == "asc" {
		dir = "ASC"
	}

	me := userFrom(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT e.id, e.user_id, trim(u.first_name || ' ' || u.last_name), e.started_at, e.duration_sec,
		       e.app, e.app_name, e.title, e.domain, e.category, e.productivity::text,
		       count(*) OVER ()
		FROM activity_events e JOIN users u ON u.id = e.user_id
		WHERE `+activityWhere+`
		  AND ($8 = '' OR e.app_name ILIKE $8 OR e.title ILIKE $8 OR e.domain ILIKE $8 OR e.category ILIKE $8)
		ORDER BY e.started_at `+dir+`, e.id `+dir+`
		LIMIT $9 OFFSET $10`, append(f.args(me), likePattern(f.search), size, page*size)...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()
	out := activityEvents{Items: []activityEvent{}}
	for rows.Next() {
		var e activityEvent
		if err := rows.Scan(&e.ID, &e.UserID, &e.UserName, &e.StartedAt, &e.Seconds, &e.App, &e.AppName,
			&e.Title, &e.Domain, &e.Category, &e.Productivity, &out.Total); err != nil {
			s.fail(w, r, err)
			return
		}
		out.Items = append(out.Items, e)
	}
	if err := rows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
