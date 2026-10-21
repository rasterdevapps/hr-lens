package api

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---------- Trends ----------

// Hours a person is expected to work on a weekday
const expectedDaySeconds = 8 * 3600

type levelTotals struct {
	Working      int64 `json:"working"`
	Productive   int64 `json:"productive"`
	Neutral      int64 `json:"neutral"`
	Unproductive int64 `json:"unproductive"`
	Away         int64 `json:"away"` // gaps between a day's first and last activity
}

type breakdownRow struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Expected int64  `json:"expected"` // seconds: 8h per person per weekday
	levelTotals
}

type timeSegment struct {
	Start        int    `json:"start"` // seconds after local midnight
	End          int    `json:"end"`
	Productivity string `json:"productivity"`
}

type appTime struct {
	Name    string  `json:"name"`
	Seconds int64   `json:"seconds"`
	Percent float64 `json:"percent"` // of the level's total
}

// dayTimeline is one person's day as stretches of one productivity level
type dayTimeline struct {
	Date     string        `json:"date"`
	Segments []timeSegment `json:"segments"`
}

// productivityBar is one bar of the Productivity Bar: seconds at each level out of capacity
type productivityBar struct {
	Start        int    `json:"start"`          // seconds after midnight, for 5-minute slots
	Date         string `json:"date,omitempty"` // for one bar per day
	Productive   int64  `json:"productive"`
	Neutral      int64  `json:"neutral"`
	Unproductive int64  `json:"unproductive"`
	Capacity     int64  `json:"capacity"` // the slot length, or 8h per day, times the people shown
}

type barsJSON struct {
	Unit  string            `json:"unit"` // "slot" (one day) or "day"
	Slot  int               `json:"slot"` // seconds per slot
	Items []productivityBar `json:"items"`
}

type categoryTime struct {
	Name    string  `json:"name"`
	Seconds int64   `json:"seconds"`
	Percent float64 `json:"percent"` // of the period's working time
}

type categoryRow struct {
	Name  string         `json:"name"`
	Cells []categoryTime `json:"cells"` // same periods as the breakdown
}

type categoriesJSON struct {
	Period    []categoryTime `json:"period"`    // the selected period, largest first
	Breakdown []categoryRow  `json:"breakdown"` // last day, day before, week to date
}

type trendsJSON struct {
	Totals       levelTotals          `json:"totals"`
	Days         []dayTimeline        `json:"days"` // one person, up to 31 days (a day shows its week), newest first; else null
	Bars         barsJSON             `json:"bars"`
	Breakdown    []breakdownRow       `json:"breakdown"`
	Categories   categoriesJSON       `json:"categories"`
	Applications map[string][]appTime `json:"applications"` // top 5 per productivity level
}

const (
	slotSeconds     = 300 // Productivity Bar slots on a single day
	maxTimelineDays = 31
)

// GET /api/productivity/trends?<activity filters>
// Breakdown rows are the period's last day, the day before, and that week up to the last day.
func (s *Server) productivityTrends(w http.ResponseWriter, r *http.Request) {
	f, msg := parseActivityFilter(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	f.productivity = "" // trends always cover every level
	me := userFrom(r)
	ctx := r.Context()

	out := trendsJSON{Applications: map[string][]appTime{"productive": {}, "neutral": {}, "unproductive": {}}}
	var err error
	if out.Totals, err = s.levelTotals(ctx, me, f); err != nil {
		s.fail(w, r, err)
		return
	}

	people, err := s.countPeople(ctx, me, f.users)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	day := f.to
	monday := day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	spans := [][2]time.Time{{day, day}, {day.AddDate(0, 0, -1), day.AddDate(0, 0, -1)}, {monday, day}}
	for _, span := range spans {
		g := f
		g.from, g.to = span[0], span[1]
		t, err := s.levelTotals(ctx, me, g)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out.Breakdown = append(out.Breakdown, breakdownRow{
			From: g.from.Format(time.DateOnly), To: g.to.Format(time.DateOnly),
			Expected: int64(people) * int64(weekdays(g.from, g.to)) * expectedDaySeconds, levelTotals: t,
		})
	}

	// Top applications per level
	rows, err := s.db.Query(ctx, `
		SELECT e.productivity::text, e.app_name, sum(e.duration_sec)
		FROM activity_events e WHERE `+activityWhere+`
		GROUP BY 1, 2 ORDER BY 3 DESC, 2`, f.args(me)...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	levelSum := map[string]int64{"productive": out.Totals.Productive, "neutral": out.Totals.Neutral, "unproductive": out.Totals.Unproductive}
	for rows.Next() {
		var level string
		var a appTime
		if err := rows.Scan(&level, &a.Name, &a.Seconds); err != nil {
			rows.Close()
			s.fail(w, r, err)
			return
		}
		if len(out.Applications[level]) < 5 && levelSum[level] > 0 {
			a.Percent = float64(a.Seconds*1000/levelSum[level]) / 10
			out.Applications[level] = append(out.Applications[level], a)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}

	if out.Categories, err = s.categoryTrends(ctx, me, f, spans); err != nil {
		s.fail(w, r, err)
		return
	}

	// Productivity Bar: 5-minute slots for one day, else a bar per day
	oneDay := f.from.Equal(f.to)
	if oneDay {
		events, err := s.dayEvents(ctx, me, f)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out.Bars = slotBars(events, int64(people))
	} else if out.Bars, err = s.dailyBars(ctx, me, f, int64(people)); err != nil {
		s.fail(w, r, err)
		return
	}

	// A timeline per day for one person: the period, or the week so far when it is one day
	if len(f.users) == 1 {
		g := f
		if oneDay {
			g.from = monday
		}
		if g.to.Sub(g.from) < maxTimelineDays*24*time.Hour {
			events, err := s.dayEvents(ctx, me, g)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			out.Days = timelines(events)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// dayEvent is one activity row placed on its local day
type dayEvent struct {
	date  string
	start int // seconds after local midnight
	secs  int
	level string
}

func (s *Server) dayEvents(ctx context.Context, me currentUser, f activityFilter) ([]dayEvent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT to_char((e.started_at AT TIME ZONE $6)::date, 'YYYY-MM-DD'),
		       extract(epoch FROM (e.started_at AT TIME ZONE $6) - (e.started_at AT TIME ZONE $6)::date)::int,
		       e.duration_sec, e.productivity::text
		FROM activity_events e WHERE `+activityWhere+`
		ORDER BY e.started_at`, f.args(me)...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (dayEvent, error) {
		var e dayEvent
		return e, row.Scan(&e.date, &e.start, &e.secs, &e.level)
	})
}

// timelines merges each day's activity into stretches of one level, bridging gaps
// of up to two minutes, newest day first.
func timelines(events []dayEvent) []dayTimeline {
	out := []dayTimeline{}
	for _, e := range events {
		if len(out) == 0 || out[len(out)-1].Date != e.date {
			out = append(out, dayTimeline{Date: e.date, Segments: []timeSegment{}})
		}
		d := &out[len(out)-1]
		if n := len(d.Segments); n > 0 && d.Segments[n-1].Productivity == e.level && e.start-d.Segments[n-1].End <= 120 {
			d.Segments[n-1].End = max(d.Segments[n-1].End, e.start+e.secs)
			continue
		}
		d.Segments = append(d.Segments, timeSegment{Start: e.start, End: e.start + e.secs, Productivity: e.level})
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// slotBars splits one day's activity into 5-minute slots, from the first to the last active slot.
func slotBars(events []dayEvent, people int64) barsJSON {
	out := barsJSON{Unit: "slot", Slot: slotSeconds, Items: []productivityBar{}}
	if len(events) == 0 {
		return out
	}
	byLevel := map[int]map[string]int64{}
	first, last := 1<<30, 0
	for _, e := range events {
		for t := e.start; t < e.start+e.secs; {
			slot := t / slotSeconds
			end := min((slot+1)*slotSeconds, e.start+e.secs)
			if byLevel[slot] == nil {
				byLevel[slot] = map[string]int64{}
			}
			byLevel[slot][e.level] += int64(end - t)
			first, last = min(first, slot), max(last, slot)
			t = end
		}
	}
	for slot := first; slot <= last; slot++ {
		l := byLevel[slot]
		out.Items = append(out.Items, productivityBar{
			Start: slot * slotSeconds, Productive: l["productive"], Neutral: l["neutral"], Unproductive: l["unproductive"],
			Capacity: slotSeconds * max(people, 1),
		})
	}
	return out
}

// dailyBars is one bar per day of the period (up to today), measured against 8 hours per person.
func (s *Server) dailyBars(ctx context.Context, me currentUser, f activityFilter, people int64) (barsJSON, error) {
	out := barsJSON{Unit: "day", Slot: 86400, Items: []productivityBar{}}
	rows, err := s.db.Query(ctx, `
		SELECT to_char((e.started_at AT TIME ZONE $6)::date, 'YYYY-MM-DD'), e.productivity::text, sum(e.duration_sec)
		FROM activity_events e WHERE `+activityWhere+`
		GROUP BY 1, 2`, f.args(me)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	byDay := map[string]map[string]int64{}
	for rows.Next() {
		var day, level string
		var secs int64
		if err := rows.Scan(&day, &level, &secs); err != nil {
			return out, err
		}
		if byDay[day] == nil {
			byDay[day] = map[string]int64{}
		}
		byDay[day][level] = secs
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	for d := f.from; !d.After(f.to); d = d.AddDate(0, 0, 1) {
		day := d.Format(time.DateOnly)
		l := byDay[day]
		out.Items = append(out.Items, productivityBar{
			Date: day, Productive: l["productive"], Neutral: l["neutral"], Unproductive: l["unproductive"],
			Capacity: expectedDaySeconds * max(people, 1),
		})
	}
	return out, nil
}

// categoryTrends is time per category for the period, and for each breakdown span.
func (s *Server) categoryTrends(ctx context.Context, me currentUser, f activityFilter, spans [][2]time.Time) (categoriesJSON, error) {
	out := categoriesJSON{Period: []categoryTime{}, Breakdown: []categoryRow{}}
	totals := func(g activityFilter) ([]categoryTime, error) {
		rows, err := s.db.Query(ctx, `
			SELECT e.category, sum(e.duration_sec) FROM activity_events e WHERE `+activityWhere+`
			GROUP BY 1 ORDER BY 2 DESC, 1`, g.args(me)...)
		if err != nil {
			return nil, err
		}
		list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (categoryTime, error) {
			var c categoryTime
			return c, row.Scan(&c.Name, &c.Seconds)
		})
		var sum int64
		for _, c := range list {
			sum += c.Seconds
		}
		for i := range list {
			list[i].Percent = float64(list[i].Seconds*1000/sum) / 10
		}
		return list, err
	}

	var err error
	if out.Period, err = totals(f); err != nil {
		return out, err
	}
	// Rows follow the selected period's order; a category missing from a span shows 0
	index := map[string]int{}
	for _, c := range out.Period {
		index[c.Name] = len(out.Breakdown)
		out.Breakdown = append(out.Breakdown, categoryRow{Name: c.Name, Cells: make([]categoryTime, len(spans))})
	}
	for i, span := range spans {
		g := f
		g.from, g.to = span[0], span[1]
		list, err := totals(g)
		if err != nil {
			return out, err
		}
		for _, c := range list {
			row, ok := index[c.Name]
			if !ok {
				row = len(out.Breakdown)
				index[c.Name] = row
				out.Breakdown = append(out.Breakdown, categoryRow{Name: c.Name, Cells: make([]categoryTime, len(spans))})
			}
			out.Breakdown[row].Cells[i] = c
		}
	}
	return out, nil
}

func (s *Server) levelTotals(ctx context.Context, me currentUser, f activityFilter) (levelTotals, error) {
	var t levelTotals
	err := s.db.QueryRow(ctx, `
		WITH d AS (
			SELECT e.user_id, (e.started_at AT TIME ZONE $6)::date AS day,
			       sum(e.duration_sec) AS working,
			       sum(e.duration_sec) FILTER (WHERE e.productivity = 'productive') AS productive,
			       sum(e.duration_sec) FILTER (WHERE e.productivity = 'neutral') AS neutral,
			       sum(e.duration_sec) FILTER (WHERE e.productivity = 'unproductive') AS unproductive,
			       extract(epoch FROM max(e.started_at + e.duration_sec * interval '1 second') - min(e.started_at)) AS span
			FROM activity_events e WHERE `+activityWhere+`
			GROUP BY 1, 2
		)
		SELECT COALESCE(sum(working), 0), COALESCE(sum(productive), 0), COALESCE(sum(neutral), 0),
		       COALESCE(sum(unproductive), 0), COALESCE(sum(greatest(span - working, 0)), 0)::bigint
		FROM d`, f.args(me)...).Scan(&t.Working, &t.Productive, &t.Neutral, &t.Unproductive, &t.Away)
	return t, err
}

// countPeople is how many of the requested people (or everyone visible) the caller may see.
func (s *Server) countPeople(ctx context.Context, me currentUser, users []int64) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `
		SELECT count(*) FROM users u
		WHERE u.status = 'active' AND `+visiblePeople+` AND (cardinality($3::bigint[]) = 0 OR u.id = ANY($3::bigint[]))`,
		string(me.Role), me.ID, users).Scan(&n)
	return n, err
}

// weekdays counts Monday to Friday from..to inclusive.
func weekdays(from, to time.Time) int {
	n := 0
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			n++
		}
	}
	return n
}

// ---------- Configure Apps: productivity rules ----------

type ruleJSON struct {
	ID           int64     `json:"id"`
	Kind         string    `json:"kind"`
	Name         string    `json:"name"`
	Detail       string    `json:"detail"`  // process name, for applications
	Browser      bool      `json:"browser"` // only ever seen with websites: those rules apply instead
	Category     string    `json:"category"`
	Productivity string    `json:"productivity"`
	Hits         int64     `json:"hits"`    // last 30 days, everyone
	Seconds      int64     `json:"seconds"` // last 30 days, everyone
	UpdatedAt    time.Time `json:"updatedAt"`
}

type ruleList struct {
	Total      int        `json:"total"`
	Page       int        `json:"page"`
	PageSize   int        `json:"pageSize"`
	Categories []string   `json:"categories"`
	Items      []ruleJSON `json:"items"`
}

// Categories offered in the editor besides the ones already in use
var defaultCategories = []string{"Browsing", "Communication", "Customer Support", "Design", "Development", "Entertainment",
	"Meetings", "News", "Office", "Project Management", "Research", "Shopping", "Social Media", "Uncategorized", "Utilities"}

var ruleSorts = map[string]string{
	"time": "seconds DESC, lower(name)",
	"hits": "hits DESC, lower(name)",
	"name": "lower(name), id",
}

// ruleMatch is true for the activity_events row "e" that rule "r" covers.
const ruleMatch = `(CASE r.kind
	WHEN 'website' THEN lower(e.domain) = lower(r.name)
	ELSE lower(e.app_name) = lower(r.name) END)`

// GET /api/productivity/rules?kind=application|website&search=&productivity=&category=&sort=time|hits|name&page=1&pageSize=50
func (s *Server) listRules(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := cmp(q.Get("kind"), "application")
	if kind != "application" && kind != "website" {
		writeError(w, http.StatusBadRequest, "kind must be application or website")
		return
	}
	level := q.Get("productivity")
	switch level {
	case "", "productive", "neutral", "unproductive":
	default:
		writeError(w, http.StatusBadRequest, "productivity must be productive, neutral or unproductive")
		return
	}
	order, ok := ruleSorts[cmp(q.Get("sort"), "time")]
	if !ok {
		writeError(w, http.StatusBadRequest, "sort must be time, hits or name")
		return
	}
	page := clamp(atoi(q.Get("page"), 1), 1, 1<<20)
	pageSize := clamp(atoi(q.Get("pageSize"), 50), 1, 200)
	out := ruleList{Page: page, PageSize: pageSize, Items: []ruleJSON{}}

	// Hits and time cover every application window, browsers included,
	// so a browser shows how much it was used even though websites are rated separately.
	rows, err := s.db.Query(r.Context(), `
		WITH x AS (
			SELECT r.id, r.kind::text, r.name, r.category, r.productivity::text, r.updated_at,
			       COALESCE(st.hits, 0) AS hits, COALESCE(st.secs, 0) AS seconds,
			       COALESCE(st.detail, '') AS detail, COALESCE(st.browser, false) AS browser
			FROM activity_rules r
			LEFT JOIN LATERAL (
				SELECT count(*) AS hits, sum(e.duration_sec) AS secs,
				       mode() WITHIN GROUP (ORDER BY e.app) AS detail,
				       r.kind = 'application' AND bool_and(e.domain IS NOT NULL) AS browser
				FROM activity_events e
				WHERE `+ruleMatch+` AND e.started_at > now() - interval '30 days'
			) st ON true
			WHERE r.kind = $1::activity_rule_kind
			  AND ($2 = '' OR r.name ILIKE $2 OR r.category ILIKE $2)
			  AND ($3 = '' OR r.productivity::text = $3)
			  AND ($4 = '' OR lower(r.category) = lower($4))
		)
		SELECT *, count(*) OVER () FROM x
		ORDER BY `+order+`
		LIMIT $5 OFFSET $6`,
		kind, likePattern(strings.TrimSpace(q.Get("search"))), level, q.Get("category"), pageSize, (page-1)*pageSize)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var it ruleJSON
		if err := rows.Scan(&it.ID, &it.Kind, &it.Name, &it.Category, &it.Productivity, &it.UpdatedAt,
			&it.Hits, &it.Seconds, &it.Detail, &it.Browser, &out.Total); err != nil {
			s.fail(w, r, err)
			return
		}
		out.Items = append(out.Items, it)
	}
	if err := rows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}

	rows, err = s.db.Query(r.Context(), `
		SELECT DISTINCT category FROM activity_rules
		UNION SELECT unnest($1::text[])
		ORDER BY 1`, defaultCategories)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if out.Categories, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type ruleInput struct {
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Category     string `json:"category"`
	Productivity string `json:"productivity"`
}

var domainPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// cleanDomain turns "https://www.Example.com/path" into "example.com".
func cleanDomain(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if _, rest, ok := strings.Cut(s, "://"); ok {
		s = rest
	}
	if i := strings.IndexAny(s, "/?#:"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimPrefix(s, "www.")
}

// validate tidies the rule and returns field errors. Kind and name are checked only when creating.
func (in *ruleInput) validate(creating bool) map[string]string {
	errs := map[string]string{}
	in.Category = strings.TrimSpace(in.Category)
	if in.Category == "" {
		in.Category = "Uncategorized"
	}
	if len(in.Category) > 60 {
		errs["category"] = "Use at most 60 characters"
	}
	switch in.Productivity {
	case "productive", "neutral", "unproductive":
	default:
		errs["productivity"] = "Choose productive, neutral or unproductive"
	}
	if !creating {
		return errs
	}
	switch in.Kind {
	case "website":
		in.Name = cleanDomain(in.Name)
		if !domainPattern.MatchString(in.Name) || len(in.Name) > 253 {
			errs["name"] = "Enter a website such as example.com"
		}
	case "application":
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" || len(in.Name) > 120 {
			errs["name"] = "Enter the application name, up to 120 characters"
		}
	default:
		errs["kind"] = "Choose application or website"
	}
	return errs
}

// applyRules re-rates the activity covered by the given rules.
func applyRules(ctx context.Context, tx pgx.Tx, ids []int64) error {
	_, err := tx.Exec(ctx, `
		UPDATE activity_events e SET category = r.category, productivity = r.productivity
		FROM activity_rules r
		WHERE r.id = ANY($1) AND (CASE r.kind
			WHEN 'website' THEN lower(e.domain) = lower(r.name)
			ELSE e.domain IS NULL AND lower(e.app_name) = lower(r.name) END)`, ids)
	return err
}

// POST /api/productivity/rules
func (s *Server) createRule(w http.ResponseWriter, r *http.Request) {
	var in ruleInput
	if !decode(w, r, &in) {
		return
	}
	if errs := in.validate(true); len(errs) > 0 {
		writeFieldErrors(w, errs)
		return
	}
	me := userFrom(r)
	var id int64
	err := pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		err := tx.QueryRow(r.Context(), `
			INSERT INTO activity_rules (kind, name, category, productivity, updated_by)
			VALUES ($1::activity_rule_kind, $2, $3, $4::activity_productivity, $5) RETURNING id`,
			in.Kind, in.Name, in.Category, in.Productivity, me.ID).Scan(&id)
		if err != nil {
			return err
		}
		return applyRules(r.Context(), tx, []int64{id})
	})
	if uniqueViolation(err) != "" {
		writeFieldErrors(w, map[string]string{"name": "There is already a rule for " + in.Name})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// PUT /api/productivity/rules/{id} — body {category, productivity}
func (s *Server) updateRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in ruleInput
	if !decode(w, r, &in) {
		return
	}
	if errs := in.validate(false); len(errs) > 0 {
		writeFieldErrors(w, errs)
		return
	}
	s.saveRules(w, r, []int64{id}, &in.Category, in.Productivity)
}

type bulkRuleInput struct {
	IDs          []int64 `json:"ids"`
	Category     *string `json:"category"` // optional: leave each rule's category as it is
	Productivity string  `json:"productivity"`
}

// PUT /api/productivity/rules — body {ids, productivity, category?}: rates several at once
func (s *Server) updateRules(w http.ResponseWriter, r *http.Request) {
	var in bulkRuleInput
	if !decode(w, r, &in) {
		return
	}
	if len(in.IDs) == 0 || len(in.IDs) > 500 {
		writeError(w, http.StatusBadRequest, "Choose between 1 and 500 rules")
		return
	}
	check := ruleInput{Productivity: in.Productivity, Category: "x"}
	if in.Category != nil {
		check.Category = *in.Category
	}
	if errs := check.validate(false); len(errs) > 0 {
		writeFieldErrors(w, errs)
		return
	}
	if in.Category != nil {
		in.Category = &check.Category
	}
	s.saveRules(w, r, in.IDs, in.Category, in.Productivity)
}

func (s *Server) saveRules(w http.ResponseWriter, r *http.Request, ids []int64, category *string, level string) {
	me := userFrom(r)
	var changed int64
	err := pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `
			UPDATE activity_rules SET category = COALESCE($2, category), productivity = $3::activity_productivity,
			       updated_at = now(), updated_by = $4
			WHERE id = ANY($1)`, ids, category, level, me.ID)
		if err != nil {
			return err
		}
		changed = tag.RowsAffected()
		return applyRules(r.Context(), tx, ids)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if changed == 0 {
		writeError(w, http.StatusNotFound, "Rule not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"updated": changed})
}
