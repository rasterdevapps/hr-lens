package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---------- Emails: scheduled report emails ----------
//
// Only the schedules are kept for now. Nothing is sent until mail server settings exist,
// which the page says plainly.

var (
	emailReports     = map[string]bool{"daily_summary": true, "attendance": true, "productivity": true, "apps_websites": true}
	emailFrequencies = map[string]bool{"daily": true, "weekly": true, "monthly": true}
	clockPattern     = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
)

const maxRecipients = 20

type emailReport struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Report     string     `json:"report"`
	Frequency  string     `json:"frequency"`
	SendTime   string     `json:"sendTime"` // HH:MM
	Weekday    *int       `json:"weekday"`
	MonthDay   *int       `json:"monthDay"`
	TimeZone   string     `json:"timeZone"`
	TeamID     *int64     `json:"teamId"`
	TeamName   string     `json:"teamName"`
	Recipients []string   `json:"recipients"`
	Enabled    bool       `json:"enabled"`
	CreatedBy  string     `json:"createdBy"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastSentAt *time.Time `json:"lastSentAt"`
}

type emailReportInput struct {
	Name       string   `json:"name"`
	Report     string   `json:"report"`
	Frequency  string   `json:"frequency"`
	SendTime   string   `json:"sendTime"`
	Weekday    *int     `json:"weekday"`
	MonthDay   *int     `json:"monthDay"`
	TimeZone   string   `json:"timeZone"`
	TeamID     *int64   `json:"teamId"`
	Recipients []string `json:"recipients"`
	Enabled    *bool    `json:"enabled"`
}

// validate tidies the input and returns field errors. The same rules are in the Emails page.
func (in *emailReportInput) validate() map[string]string {
	errs := map[string]string{}
	in.Name = strings.TrimSpace(in.Name)
	in.TimeZone = cmp(strings.TrimSpace(in.TimeZone), "Asia/Kolkata")
	switch {
	case in.Name == "":
		errs["name"] = "Name is required"
	case len(in.Name) > 100:
		errs["name"] = "Use at most 100 characters"
	}
	if !emailReports[in.Report] {
		errs["report"] = "Choose a report"
	}
	if !emailFrequencies[in.Frequency] {
		errs["frequency"] = "Choose daily, weekly or monthly"
	}
	if !clockPattern.MatchString(in.SendTime) {
		errs["sendTime"] = "Enter a time such as 09:00"
	}
	switch in.Frequency {
	case "weekly":
		if in.Weekday == nil || *in.Weekday < 0 || *in.Weekday > 6 {
			errs["weekday"] = "Choose a day of the week"
		}
		in.MonthDay = nil
	case "monthly":
		if in.MonthDay == nil || *in.MonthDay < 1 || *in.MonthDay > 28 {
			errs["monthDay"] = "Choose a day from 1 to 28"
		}
		in.Weekday = nil
	default:
		in.Weekday, in.MonthDay = nil, nil
	}
	if _, err := time.LoadLocation(in.TimeZone); err != nil {
		errs["timeZone"] = "Unknown time zone, use a name like Asia/Kolkata"
	}

	seen := map[string]bool{}
	clean := make([]string, 0, len(in.Recipients))
	for _, raw := range in.Recipients {
		addr := strings.ToLower(strings.TrimSpace(raw))
		if addr == "" || seen[addr] {
			continue
		}
		seen[addr] = true
		parsed, err := mail.ParseAddress(addr)
		_, domain, _ := strings.Cut(addr, "@")
		if err != nil || parsed.Address != addr || !strings.Contains(domain, ".") || len(addr) > 254 {
			errs["recipients"] = fmt.Sprintf("%s is not a valid email address", raw)
			break
		}
		clean = append(clean, addr)
	}
	in.Recipients = clean
	if _, bad := errs["recipients"]; !bad {
		switch {
		case len(clean) == 0:
			errs["recipients"] = "Add at least one recipient"
		case len(clean) > maxRecipients:
			errs["recipients"] = fmt.Sprintf("Use at most %d recipients", maxRecipients)
		}
	}
	return errs
}

const emailReportSelect = `
	SELECT r.id, r.name, r.report, r.frequency, to_char(r.send_time, 'HH24:MI'), r.weekday, r.month_day,
	       r.time_zone, r.team_id, COALESCE(t.name, ''), r.recipients, r.enabled,
	       COALESCE(trim(u.first_name || ' ' || u.last_name), ''), r.created_at, r.last_sent_at
	FROM email_reports r
	LEFT JOIN teams t ON t.id = r.team_id
	LEFT JOIN users u ON u.id = r.created_by`

func scanEmailReport(row pgx.CollectableRow) (emailReport, error) {
	var e emailReport
	var weekday, monthDay *int16
	err := row.Scan(&e.ID, &e.Name, &e.Report, &e.Frequency, &e.SendTime, &weekday, &monthDay,
		&e.TimeZone, &e.TeamID, &e.TeamName, &e.Recipients, &e.Enabled, &e.CreatedBy, &e.CreatedAt, &e.LastSentAt)
	if weekday != nil {
		v := int(*weekday)
		e.Weekday = &v
	}
	if monthDay != nil {
		v := int(*monthDay)
		e.MonthDay = &v
	}
	return e, err
}

// GET /api/email-reports
func (s *Server) listEmailReports(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), emailReportSelect+` ORDER BY lower(r.name), r.id`)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := pgx.CollectRows(rows, scanEmailReport)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if list == nil {
		list = []emailReport{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "mailConfigured": false})
}

func (s *Server) findEmailReport(r *http.Request, id int64) (emailReport, error) {
	rows, err := s.db.Query(r.Context(), emailReportSelect+` WHERE r.id = $1`, id)
	if err != nil {
		return emailReport{}, err
	}
	return pgx.CollectExactlyOneRow(rows, scanEmailReport)
}

// POST /api/email-reports
func (s *Server) createEmailReport(w http.ResponseWriter, r *http.Request) {
	var in emailReportInput
	if !decode(w, r, &in) {
		return
	}
	if errs := in.validate(); len(errs) > 0 {
		writeFieldErrors(w, errs)
		return
	}
	var id int64
	err := s.db.QueryRow(r.Context(), `
		INSERT INTO email_reports (name, report, frequency, send_time, weekday, month_day, time_zone, team_id, recipients, enabled, created_by)
		VALUES ($1, $2, $3, $4::time, $5, $6, $7, $8, $9, $10, $11) RETURNING id`,
		in.Name, in.Report, in.Frequency, in.SendTime, in.Weekday, in.MonthDay, in.TimeZone, in.TeamID, in.Recipients,
		in.Enabled == nil || *in.Enabled, userFrom(r).ID).Scan(&id)
	if s.emailWriteError(w, r, err) {
		return
	}
	s.answerEmailReport(w, r, id, http.StatusCreated)
}

// PUT /api/email-reports/{id}
func (s *Server) updateEmailReport(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in emailReportInput
	if !decode(w, r, &in) {
		return
	}
	if errs := in.validate(); len(errs) > 0 {
		writeFieldErrors(w, errs)
		return
	}
	tag, err := s.db.Exec(r.Context(), `
		UPDATE email_reports SET name = $2, report = $3, frequency = $4, send_time = $5::time, weekday = $6, month_day = $7,
		       time_zone = $8, team_id = $9, recipients = $10, enabled = COALESCE($11, enabled), updated_at = now()
		WHERE id = $1`,
		id, in.Name, in.Report, in.Frequency, in.SendTime, in.Weekday, in.MonthDay, in.TimeZone, in.TeamID, in.Recipients, in.Enabled)
	if s.emailWriteError(w, r, err) {
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "Report email not found")
		return
	}
	s.answerEmailReport(w, r, id, http.StatusOK)
}

// PUT /api/email-reports/{id}/enabled — body {enabled}
func (s *Server) setEmailReportEnabled(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	tag, err := s.db.Exec(r.Context(), `UPDATE email_reports SET enabled = $2, updated_at = now() WHERE id = $1`, id, body.Enabled)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "Report email not found")
		return
	}
	s.answerEmailReport(w, r, id, http.StatusOK)
}

// DELETE /api/email-reports/{id}
func (s *Server) deleteEmailReport(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tag, err := s.db.Exec(r.Context(), `DELETE FROM email_reports WHERE id = $1`, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "Report email not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) answerEmailReport(w http.ResponseWriter, r *http.Request, id int64, status int) {
	e, err := s.findEmailReport(r, id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "Report email not found")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, status, e)
}

func (s *Server) emailWriteError(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case err == nil:
		return false
	case foreignKeyViolation(err):
		writeFieldErrors(w, map[string]string{"teamId": "This team no longer exists"})
	default:
		s.fail(w, r, err)
	}
	return true
}
