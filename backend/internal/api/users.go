package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"hrlens/backend/internal/auth"
)

type ref struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type userJSON struct {
	ID              int64      `json:"id"`
	EmployeeCode    string     `json:"employeeCode"`
	FirstName       string     `json:"firstName"`
	LastName        string     `json:"lastName"`
	Name            string     `json:"name"`
	Email           string     `json:"email"`
	Mobile          string     `json:"mobile"`
	Role            string     `json:"role"`
	Status          string     `json:"status"`
	Team            *ref       `json:"team"`
	Department      *ref       `json:"department"`
	Branch          *ref       `json:"branch"`
	Manager         *ref       `json:"manager"`
	Shift           *ref       `json:"shift"`
	TimeZone        string     `json:"timeZone"`
	TrackAttendance bool       `json:"trackAttendance"`
	AdminGroups     []int64    `json:"adminGroups"`
	LastActiveAt    *time.Time `json:"lastActiveAt"`
	Desktop         *string    `json:"desktop"`  // computer running the agent, most recently seen
	HasPhoto        bool       `json:"hasPhoto"` // profile photo at GET /api/users/{id}/photo
}

const userSelect = `
SELECT u.id, u.employee_code, u.first_name, u.last_name, u.email, u.mobile, u.role::text, u.status::text,
       u.team_id, t.name, u.department_id, d.name, u.branch_id, b.name,
       u.manager_id, trim(m.first_name || ' ' || m.last_name), u.shift_id, sh.name,
       u.time_zone, u.track_attendance, u.last_active_at,
       COALESCE((SELECT array_agg(g.team_id ORDER BY g.team_id) FROM user_admin_groups g WHERE g.user_id = u.id), '{}'::bigint[]),
       (SELECT dv.name FROM devices dv WHERE dv.user_id = u.id AND dv.revoked_at IS NULL ORDER BY dv.last_seen_at DESC NULLS LAST LIMIT 1),
       EXISTS (SELECT 1 FROM user_profiles p WHERE p.user_id = u.id AND p.photo_path IS NOT NULL)
FROM users u
LEFT JOIN teams t ON t.id = u.team_id
LEFT JOIN departments d ON d.id = u.department_id
LEFT JOIN branches b ON b.id = u.branch_id
LEFT JOIN users m ON m.id = u.manager_id
LEFT JOIN shifts sh ON sh.id = u.shift_id`

func scanUser(row pgx.Row) (userJSON, error) {
	var u userJSON
	var teamID, deptID, branchID, managerID, shiftID *int64
	var team, dept, branch, manager, shift *string
	err := row.Scan(&u.ID, &u.EmployeeCode, &u.FirstName, &u.LastName, &u.Email, &u.Mobile, &u.Role, &u.Status,
		&teamID, &team, &deptID, &dept, &branchID, &branch, &managerID, &manager, &shiftID, &shift,
		&u.TimeZone, &u.TrackAttendance, &u.LastActiveAt, &u.AdminGroups, &u.Desktop, &u.HasPhoto)
	if err != nil {
		return u, err
	}
	u.Name = strings.TrimSpace(u.FirstName + " " + u.LastName)
	u.Team, u.Department, u.Branch, u.Manager = newRef(teamID, team), newRef(deptID, dept), newRef(branchID, branch), newRef(managerID, manager)
	u.Shift = newRef(shiftID, shift)
	return u, nil
}

func newRef(id *int64, name *string) *ref {
	if id == nil || name == nil {
		return nil
	}
	return &ref{ID: *id, Name: *name}
}

func (s *Server) findUser(ctx context.Context, id int64) (userJSON, error) {
	return scanUser(s.db.QueryRow(ctx, userSelect+` WHERE u.id = $1`, id))
}

// ---------- List ----------

var userSort = map[string]string{
	"id":         "lower(u.employee_code)",
	"name":       "lower(u.first_name), lower(u.last_name)",
	"username":   "lower(u.email)",
	"role":       "u.role",
	"manager":    "lower(m.first_name)",
	"team":       "lower(t.name)",
	"lastActive": "u.last_active_at",
}

type userList struct {
	Items    []userJSON `json:"items"`
	Total    int        `json:"total"`
	Page     int        `json:"page"`
	PageSize int        `json:"pageSize"`
}

// GET /api/users?status=active|inactive|all&search=&sort=name&dir=asc|desc&page=1&pageSize=100
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var where []string
	var args []any
	switch status := q.Get("status"); status {
	case "active", "inactive":
		args = append(args, status)
		where = append(where, fmt.Sprintf("u.status = $%d::user_status", len(args)))
	case "", "all":
	default:
		writeError(w, http.StatusBadRequest, "status must be active, inactive or all")
		return
	}
	if search := strings.TrimSpace(q.Get("search")); search != "" {
		args = append(args, "%"+likeEscape(search)+"%")
		n := len(args)
		where = append(where, fmt.Sprintf(`(u.employee_code ILIKE $%[1]d OR u.first_name || ' ' || u.last_name ILIKE $%[1]d
			OR u.email ILIKE $%[1]d OR t.name ILIKE $%[1]d OR m.first_name || ' ' || m.last_name ILIKE $%[1]d)`, n))
	}
	filter := ""
	if len(where) > 0 {
		filter = " WHERE " + strings.Join(where, " AND ")
	}

	order, ok := userSort[cmp(q.Get("sort"), "name")]
	if !ok {
		writeError(w, http.StatusBadRequest, "Unknown sort column")
		return
	}
	dir := "ASC"
	if q.Get("dir") == "desc" {
		dir = "DESC"
	}
	order = strings.ReplaceAll(order, ",", " "+dir+",") + " " + dir + " NULLS LAST, u.id"

	page := clamp(atoi(q.Get("page"), 1), 1, 1_000_000)
	pageSize := clamp(atoi(q.Get("pageSize"), 100), 1, 200)

	list := userList{Items: []userJSON{}, Page: page, PageSize: pageSize}
	countSQL := `SELECT count(*) FROM users u LEFT JOIN teams t ON t.id = u.team_id LEFT JOIN users m ON m.id = u.manager_id` + filter
	if err := s.db.QueryRow(r.Context(), countSQL, args...).Scan(&list.Total); err != nil {
		s.fail(w, r, err)
		return
	}

	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := s.db.Query(r.Context(),
		fmt.Sprintf("%s%s ORDER BY %s LIMIT $%d OFFSET $%d", userSelect, filter, order, len(args)-1, len(args)), args...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		list.Items = append(list.Items, u)
	}
	if err := rows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	u, err := s.findUser(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// ---------- Create / update ----------

type userInput struct {
	EmployeeCode    string  `json:"employeeCode"`
	FirstName       string  `json:"firstName"`
	LastName        string  `json:"lastName"`
	Email           string  `json:"email"`
	Mobile          string  `json:"mobile"`
	Role            string  `json:"role"`
	TeamID          *int64  `json:"teamId"`
	DepartmentID    *int64  `json:"departmentId"`
	BranchID        *int64  `json:"branchId"`
	ManagerID       *int64  `json:"managerId"`
	ShiftID         *int64  `json:"shiftId"`
	TimeZone        string  `json:"timeZone"`
	TrackAttendance *bool   `json:"trackAttendance"`
	AdminGroups     []int64 `json:"adminGroups"`
	Password        string  `json:"password"` // create only
}

// The same rules are in screen-management/src/app/pages/shared/validation.ts, which checks while typing.
var (
	namePattern   = regexp.MustCompile(`^[\p{L}\p{M}][\p{L}\p{M} .'-]*$`)
	codePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	mobilePattern = regexp.MustCompile(`^[0-9]+$`)
)

func (in *userInput) validate() map[string]string {
	in.EmployeeCode = strings.TrimSpace(in.EmployeeCode)
	in.FirstName = strings.TrimSpace(in.FirstName)
	in.LastName = strings.TrimSpace(in.LastName)
	in.Email = strings.TrimSpace(in.Email)
	in.Mobile = strings.TrimSpace(in.Mobile)
	in.TimeZone = cmp(strings.TrimSpace(in.TimeZone), "Asia/Kolkata")
	in.Role = cmp(in.Role, string(auth.User))

	errs := map[string]string{}
	required := func(field, value string, max int) {
		if value == "" {
			errs[field] = "Required"
		} else if len(value) > max {
			errs[field] = fmt.Sprintf("Use at most %d characters", max)
		}
	}
	required("employeeCode", in.EmployeeCode, 50)
	required("firstName", in.FirstName, 100)
	required("lastName", in.LastName, 100)
	required("email", in.Email, 254)
	pattern := func(field, value string, re *regexp.Regexp, message string) {
		if _, bad := errs[field]; !bad && !re.MatchString(value) {
			errs[field] = message
		}
	}
	pattern("firstName", in.FirstName, namePattern, "Use letters, spaces, apostrophes, dots or hyphens only")
	pattern("lastName", in.LastName, namePattern, "Use letters, spaces, apostrophes, dots or hyphens only")
	pattern("employeeCode", in.EmployeeCode, codePattern, "Use letters, numbers, dots, dashes, underscores or slashes, without spaces")
	if _, bad := errs["email"]; !bad {
		addr, err := mail.ParseAddress(in.Email)
		_, domain, _ := strings.Cut(in.Email, "@")
		if err != nil || addr.Address != in.Email || !strings.Contains(domain, ".") {
			errs["email"] = "Enter a valid email address, such as name@company.com"
		}
	}
	switch {
	case in.Mobile == "":
	case !mobilePattern.MatchString(in.Mobile):
		errs["mobile"] = "Use numbers only"
	case len(in.Mobile) < 7 || len(in.Mobile) > 15:
		errs["mobile"] = "Enter 7 to 15 digits"
	}
	if !auth.ValidRole(in.Role) {
		errs["role"] = "Choose Super Admin, Manager or User"
	}
	if _, err := time.LoadLocation(in.TimeZone); err != nil {
		errs["timeZone"] = "Unknown time zone, use a name like Asia/Kolkata"
	}
	return errs
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var in userInput
	if !decode(w, r, &in) {
		return
	}
	errs := in.validate()
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		errs["password"] = "Use 8 to 72 characters"
	}
	if len(errs) > 0 {
		writeFieldErrors(w, errs)
		return
	}

	var id int64
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		err := tx.QueryRow(r.Context(), `
			INSERT INTO users (employee_code, first_name, last_name, email, mobile, password_hash, role,
			                   team_id, department_id, branch_id, manager_id, time_zone, track_attendance, shift_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7::user_role, $8, $9, $10, $11, $12, $13, $14)
			RETURNING id`,
			in.EmployeeCode, in.FirstName, in.LastName, in.Email, in.Mobile, hash, in.Role,
			in.TeamID, in.DepartmentID, in.BranchID, in.ManagerID, in.TimeZone, in.TrackAttendance == nil || *in.TrackAttendance, in.ShiftID,
		).Scan(&id)
		if err != nil {
			return err
		}
		return setAdminGroups(r.Context(), tx, id, in.AdminGroups)
	})
	if s.userWriteError(w, r, err) {
		return
	}

	u, err := s.findUser(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in userInput
	if !decode(w, r, &in) {
		return
	}
	errs := in.validate()
	if in.Password != "" {
		errs["password"] = "Passwords are changed separately"
	}
	if in.ManagerID != nil && *in.ManagerID == id {
		errs["managerId"] = "A user can't be their own manager"
	}
	if len(errs) > 0 {
		writeFieldErrors(w, errs)
		return
	}

	current, err := s.findUser(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if id == userFrom(r).ID && in.Role != current.Role {
		writeFieldErrors(w, map[string]string{"role": "You can't change your own role"})
		return
	}

	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `
			UPDATE users SET employee_code = $2, first_name = $3, last_name = $4, email = $5, mobile = $6,
			       role = $7::user_role, team_id = $8, department_id = $9, branch_id = $10, manager_id = $11,
			       time_zone = $12, track_attendance = $13, shift_id = $14, updated_at = now()
			WHERE id = $1`,
			id, in.EmployeeCode, in.FirstName, in.LastName, in.Email, in.Mobile, in.Role,
			in.TeamID, in.DepartmentID, in.BranchID, in.ManagerID, in.TimeZone,
			(in.TrackAttendance == nil && current.TrackAttendance) || (in.TrackAttendance != nil && *in.TrackAttendance), in.ShiftID)
		if err != nil {
			return err
		}
		return setAdminGroups(r.Context(), tx, id, in.AdminGroups)
	})
	if s.userWriteError(w, r, err) {
		return
	}

	u, err := s.findUser(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func setAdminGroups(ctx context.Context, tx pgx.Tx, userID int64, teamIDs []int64) error {
	if _, err := tx.Exec(ctx, `DELETE FROM user_admin_groups WHERE user_id = $1`, userID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO user_admin_groups (user_id, team_id)
		SELECT $1, t FROM unnest($2::bigint[]) AS t ON CONFLICT DO NOTHING`, userID, teamIDs)
	return err
}

// userWriteError answers for a failed insert/update and reports whether it did.
func (s *Server) userWriteError(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case err == nil:
		return false
	case uniqueViolation(err) == "users_email_key":
		writeJSON(w, http.StatusConflict, errorBody{Error: "Check the highlighted fields",
			Fields: map[string]string{"email": "Another user already has this email"}})
	case uniqueViolation(err) == "users_employee_code_key":
		writeJSON(w, http.StatusConflict, errorBody{Error: "Check the highlighted fields",
			Fields: map[string]string{"employeeCode": "Another user already has this Id"}})
	case foreignKeyViolation(err):
		writeError(w, http.StatusUnprocessableEntity, "The chosen team, department, branch, manager, shift or admin group no longer exists")
	default:
		s.fail(w, r, err)
	}
	return true
}

// ---------- Status / password ----------

func (s *Server) setUserStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Status != "active" && req.Status != "inactive" {
		writeError(w, http.StatusBadRequest, "status must be active or inactive")
		return
	}
	if id == userFrom(r).ID {
		writeError(w, http.StatusUnprocessableEntity, "You can't deactivate your own account")
		return
	}

	// Deactivating also signs the user out everywhere
	tag, err := s.db.Exec(r.Context(), `
		WITH gone AS (DELETE FROM sessions WHERE user_id = $1 AND $2 = 'inactive')
		UPDATE users SET status = $2::user_status, updated_at = now() WHERE id = $1`, id, req.Status)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setUserPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeFieldErrors(w, map[string]string{"password": "Use 8 to 72 characters"})
		return
	}

	// Signs the user out everywhere, except the admin's own current session
	tag, err := s.db.Exec(r.Context(), `
		WITH gone AS (DELETE FROM sessions WHERE user_id = $1 AND id <> $3)
		UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, id, hash, userFrom(r).SessionID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- Small helpers ----------

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func cmp(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func atoi(s string, fallback int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return fallback
}

func clamp(n, lo, hi int) int {
	return max(lo, min(hi, n))
}
