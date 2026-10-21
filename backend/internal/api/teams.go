package api

import (
	"net/http"
	"strings"
	"time"

	"hrlens/backend/internal/auth"
)

// visiblePeople is the rule for who a signed-in user may see:
// a super admin sees everyone, a manager sees their own team, the teams they
// administer and their direct reports, and a user sees only themselves.
// It expects the users table as "u" and takes ($1 role, $2 user id).
const visiblePeople = `(
	$1 = 'super_admin'
	OR u.id = $2
	OR ($1 = 'manager' AND (
		u.manager_id = $2
		OR u.team_id = (SELECT team_id FROM users WHERE id = $2)
		OR u.team_id IN (SELECT team_id FROM user_admin_groups WHERE user_id = $2)
	))
)`

type person struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Online bool   `json:"online"` // active in the last 5 minutes
	TeamID *int64 `json:"teamId"`
}

type teamPeople struct {
	ID      int64    `json:"id"`
	Name    string   `json:"name"`
	Members []person `json:"members"`
}

type peopleJSON struct {
	Employees   []person     `json:"employees"`
	Teams       []teamPeople `json:"teams"`
	WithoutTeam int          `json:"withoutTeam"`
}

// GET /api/people — data for the Employees / Teams panel
func (s *Server) people(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT u.id, trim(u.first_name || ' ' || u.last_name),
		       COALESCE(u.last_active_at > now() - interval '5 minutes', false), t.id, COALESCE(t.name, '')
		FROM users u LEFT JOIN teams t ON t.id = u.team_id AND t.status = 'active'
		WHERE u.status = 'active' AND `+visiblePeople+`
		ORDER BY lower(u.first_name), lower(u.last_name), u.id`, string(me.Role), me.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()

	out := peopleJSON{Employees: []person{}, Teams: []teamPeople{}}
	index := map[int64]int{}
	for rows.Next() {
		var p person
		var team string
		if err := rows.Scan(&p.ID, &p.Name, &p.Online, &p.TeamID, &team); err != nil {
			s.fail(w, r, err)
			return
		}
		out.Employees = append(out.Employees, p)
		if p.TeamID == nil {
			out.WithoutTeam++
			continue
		}
		i, seen := index[*p.TeamID]
		if !seen {
			i = len(out.Teams)
			index[*p.TeamID] = i
			out.Teams = append(out.Teams, teamPeople{ID: *p.TeamID, Name: team})
		}
		out.Teams[i].Members = append(out.Teams[i].Members, p)
	}
	if err := rows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- Teams ----------

type teamJSON struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Members   int       `json:"members"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}

// GET /api/teams?status=active|inactive — a manager gets only the teams they can see people in
func (s *Server) listTeams(w http.ResponseWriter, r *http.Request) {
	status := cmp(r.URL.Query().Get("status"), "active")
	if status != "active" && status != "inactive" {
		writeError(w, http.StatusBadRequest, "status must be active or inactive")
		return
	}
	me := userFrom(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT t.id, t.name, count(u.id), t.status::text, t.created_at
		FROM teams t
		LEFT JOIN users u ON u.team_id = t.id AND u.status = 'active' AND `+visiblePeople+`
		WHERE t.status = $3::team_status
		GROUP BY t.id
		HAVING $1 = 'super_admin' OR count(u.id) > 0
		ORDER BY lower(t.name)`, string(me.Role), me.ID, status)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()

	teams := []teamJSON{}
	for rows.Next() {
		var t teamJSON
		if err := rows.Scan(&t.ID, &t.Name, &t.Members, &t.Status, &t.CreatedAt); err != nil {
			s.fail(w, r, err)
			return
		}
		teams = append(teams, t)
	}
	if err := rows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, teams)
}

type teamInput struct {
	Name string `json:"name"`
}

func (in *teamInput) valid(w http.ResponseWriter) bool {
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		writeFieldErrors(w, map[string]string{"name": "Required"})
	case len(in.Name) > 100:
		writeFieldErrors(w, map[string]string{"name": "Use at most 100 characters"})
	default:
		return true
	}
	return false
}

func (s *Server) teamWriteError(w http.ResponseWriter, r *http.Request, err error) {
	if uniqueViolation(err) == "teams_name_key" {
		writeJSON(w, http.StatusConflict, errorBody{Error: "Check the highlighted fields",
			Fields: map[string]string{"name": "A team with this name already exists"}})
		return
	}
	s.fail(w, r, err)
}

func (s *Server) createTeam(w http.ResponseWriter, r *http.Request) {
	var in teamInput
	if !decode(w, r, &in) || !in.valid(w) {
		return
	}
	t := teamJSON{Name: in.Name}
	err := s.db.QueryRow(r.Context(), `INSERT INTO teams (name) VALUES ($1) RETURNING id, status::text, created_at`, in.Name).
		Scan(&t.ID, &t.Status, &t.CreatedAt)
	if err != nil {
		s.teamWriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) renameTeam(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in teamInput
	if !decode(w, r, &in) || !in.valid(w) {
		return
	}
	tag, err := s.db.Exec(r.Context(), `UPDATE teams SET name = $2 WHERE id = $1`, id, in.Name)
	if err != nil {
		s.teamWriteError(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "Team not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PUT /api/teams/{id}/status — archives or restores a team. Members keep their team,
// but an archived team is hidden from the Employees / Teams panel and the user form.
func (s *Server) setTeamStatus(w http.ResponseWriter, r *http.Request) {
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
	tag, err := s.db.Exec(r.Context(), `UPDATE teams SET status = $2::team_status WHERE id = $1`, id, req.Status)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "Team not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/teams/{id} — members stay, but are left without a team
func (s *Server) deleteTeam(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tag, err := s.db.Exec(r.Context(), `DELETE FROM teams WHERE id = $1`, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "Team not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- Lookups ----------

type roleJSON struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type lookupsJSON struct {
	Roles       []roleJSON `json:"roles"`
	Teams       []ref      `json:"teams"`
	Departments []ref      `json:"departments"`
	Branches    []ref      `json:"branches"`
	Managers    []ref      `json:"managers"`
	Shifts      []ref      `json:"shifts"`
}

// GET /api/lookups — the choices for the User Profile form
func (s *Server) lookups(w http.ResponseWriter, r *http.Request) {
	out := lookupsJSON{Roles: []roleJSON{
		{string(auth.SuperAdmin), "Super Admin"}, {string(auth.Manager), "Manager"}, {string(auth.User), "User"},
	}}
	lists := []struct {
		dst *[]ref
		sql string
	}{
		{&out.Teams, `SELECT id, name FROM teams WHERE status = 'active' ORDER BY lower(name)`},
		{&out.Departments, `SELECT id, name FROM departments ORDER BY lower(name)`},
		{&out.Branches, `SELECT id, name FROM branches ORDER BY lower(name)`},
		{&out.Managers, `SELECT id, trim(first_name || ' ' || last_name) FROM users WHERE status = 'active' ORDER BY 2`},
		{&out.Shifts, `SELECT id, name FROM shifts ORDER BY start_minute, lower(name)`},
	}
	for _, l := range lists {
		*l.dst = []ref{}
		rows, err := s.db.Query(r.Context(), l.sql)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		for rows.Next() {
			var item ref
			if err := rows.Scan(&item.ID, &item.Name); err != nil {
				rows.Close()
				s.fail(w, r, err)
				return
			}
			*l.dst = append(*l.dst, item)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// createLookup and deleteLookup manage the simple name lists (departments, branches).
// table is fixed in the route setup, never taken from the request.
func (s *Server) createLookup(table string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in teamInput
		if !decode(w, r, &in) || !in.valid(w) {
			return
		}
		item := ref{Name: in.Name}
		err := s.db.QueryRow(r.Context(), `INSERT INTO `+table+` (name) VALUES ($1) RETURNING id`, in.Name).Scan(&item.ID)
		if uniqueViolation(err) != "" {
			writeJSON(w, http.StatusConflict, errorBody{Error: "Check the highlighted fields",
				Fields: map[string]string{"name": "This name already exists"}})
			return
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	}
}

// Users that had the deleted department or branch are left without one.
func (s *Server) deleteLookup(table string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		tag, err := s.db.Exec(r.Context(), `DELETE FROM `+table+` WHERE id = $1`, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if tag.RowsAffected() == 0 {
			writeError(w, http.StatusNotFound, "Not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
