package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"hrlens/backend/internal/auth"
)

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decode(w, r, &req) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "Enter your email and password")
		return
	}

	key := clientIP(r) + "|" + email
	if ok, wait := s.limiter.Allowed(key); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "Too many attempts. Try again later.")
		return
	}

	var id int64
	var hash, status string
	err := s.db.QueryRow(r.Context(),
		`SELECT id, password_hash, status::text FROM users WHERE lower(email) = $1`, email).Scan(&id, &hash, &status)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.fail(w, r, err)
		return
	}
	// Same answer for a wrong password and an unknown email
	if !auth.CheckPassword(hash, req.Password) {
		s.limiter.Fail(key)
		writeError(w, http.StatusUnauthorized, "Email or password is incorrect")
		return
	}
	s.limiter.Reset(key)
	// Only someone who knows the password learns that the account is switched off
	if status != "active" {
		writeError(w, http.StatusForbidden, "This account is inactive. Ask your administrator to activate it.")
		return
	}

	token, tokenHash, err := auth.NewToken()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	expires := time.Now().Add(s.cfg.SessionTTL)
	_, err = s.db.Exec(r.Context(), `
		WITH expired AS (DELETE FROM sessions WHERE expires_at < now())
		INSERT INTO sessions (token_hash, user_id, expires_at, user_agent, ip) VALUES ($1, $2, $3, $4, $5)`,
		tokenHash, id, expires, truncate(r.UserAgent(), 300), clientIP(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}

	u, err := s.findUser(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.setCookie(w, token, expires)
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if _, err := s.db.Exec(r.Context(), `DELETE FROM sessions WHERE id = $1`, userFrom(r).SessionID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, err := s.findUser(r.Context(), userFrom(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (s *Server) changeOwnPassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if !decode(w, r, &req) {
		return
	}
	me := userFrom(r)
	if !s.checkCurrentPassword(w, r, req.CurrentPassword) {
		return
	}
	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeFieldErrors(w, map[string]string{"newPassword": "Use 8 to 72 characters"})
		return
	}

	// Signs out every other device
	_, err = s.db.Exec(r.Context(), `
		WITH others AS (DELETE FROM sessions WHERE user_id = $1 AND id <> $2)
		UPDATE users SET password_hash = $3, updated_at = now() WHERE id = $1`,
		me.ID, me.SessionID, newHash)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/auth/password/verify — body {currentPassword}; 204 when it is right, so the form can say so before saving
func (s *Server) verifyOwnPassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if !decode(w, r, &req) {
		return
	}
	if s.checkCurrentPassword(w, r, req.CurrentPassword) {
		w.WriteHeader(http.StatusNoContent)
	}
}

// checkCurrentPassword answers with an error and returns false unless password is the signed-in person's.
// Wrong guesses count towards the same limit as sign-ins, so a stolen session cannot be used to guess it.
func (s *Server) checkCurrentPassword(w http.ResponseWriter, r *http.Request, password string) bool {
	me := userFrom(r)
	if password == "" {
		writeFieldErrors(w, map[string]string{"currentPassword": "Enter your current password"})
		return false
	}
	key := "password|" + strconv.FormatInt(me.ID, 10)
	if ok, wait := s.limiter.Allowed(key); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "Too many attempts. Try again later.")
		return false
	}
	var hash string
	if err := s.db.QueryRow(r.Context(), `SELECT password_hash FROM users WHERE id = $1`, me.ID).Scan(&hash); err != nil {
		s.fail(w, r, err)
		return false
	}
	if !auth.CheckPassword(hash, password) {
		s.limiter.Fail(key)
		writeFieldErrors(w, map[string]string{"currentPassword": "Current password is incorrect"})
		return false
	}
	s.limiter.Reset(key)
	return true
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return strings.ToValidUTF8(s[:max], "")
}
