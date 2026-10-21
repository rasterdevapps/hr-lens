// Package api is the HTTP layer: routing, sign-in checks and JSON handlers.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"hrlens/backend/internal/auth"
	"hrlens/backend/internal/config"
)

const (
	sessionCookie = "hrlens_session"
	maxBodyBytes  = 1 << 20
)

type Server struct {
	cfg     config.Config
	db      *pgxpool.Pool
	log     *slog.Logger
	limiter *auth.Limiter
	live    *liveHub
}

func New(cfg config.Config, db *pgxpool.Pool, log *slog.Logger) *Server {
	return &Server{cfg: cfg, db: db, log: log, limiter: auth.NewLimiter(5, 15*time.Minute), live: newLiveHub()}
}

func (s *Server) Handler() http.Handler {
	everyone := []auth.Role{auth.SuperAdmin, auth.Manager, auth.User}
	managers := []auth.Role{auth.SuperAdmin, auth.Manager}
	admin := []auth.Role{auth.SuperAdmin}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)

	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.Handle("POST /api/auth/logout", s.require(everyone, s.logout))
	mux.Handle("GET /api/auth/me", s.require(everyone, s.me))
	mux.Handle("POST /api/auth/password", s.require(everyone, s.changeOwnPassword))
	mux.Handle("POST /api/auth/password/verify", s.require(everyone, s.verifyOwnPassword))

	// Employees / Teams panel, limited to what the signed-in role may see
	mux.Handle("GET /api/people", s.require(everyone, s.people))

	// Profile: the signed-in person's own page
	mux.Handle("GET /api/profile", s.require(everyone, s.getProfile))
	mux.Handle("PUT /api/profile", s.require(everyone, s.updateProfile))
	mux.Handle("GET /api/profile/photo", s.require(everyone, s.profilePhoto))
	mux.Handle("POST /api/profile/photo", s.require(everyone, s.setProfilePhoto))
	mux.Handle("DELETE /api/profile/photo", s.require(everyone, s.removeProfilePhoto))
	mux.Handle("GET /api/users/{id}/photo", s.require(everyone, s.profilePhoto))
	mux.Handle("POST /api/profile/documents", s.require(everyone, s.createDocument))
	mux.Handle("PUT /api/profile/documents/{id}", s.require(everyone, s.updateDocument))
	mux.Handle("DELETE /api/profile/documents/{id}", s.require(everyone, s.deleteDocument))
	mux.Handle("GET /api/profile/documents/{id}/file", s.require(everyone, s.documentFile))

	mux.Handle("GET /api/users", s.require(admin, s.listUsers))
	mux.Handle("POST /api/users", s.require(admin, s.createUser))
	mux.Handle("GET /api/users/{id}", s.require(admin, s.getUser))
	mux.Handle("PUT /api/users/{id}", s.require(admin, s.updateUser))
	mux.Handle("PUT /api/users/{id}/status", s.require(admin, s.setUserStatus))
	mux.Handle("PUT /api/users/{id}/password", s.require(admin, s.setUserPassword))

	mux.Handle("GET /api/teams", s.require(managers, s.listTeams))
	mux.Handle("POST /api/teams", s.require(admin, s.createTeam))
	mux.Handle("PUT /api/teams/{id}", s.require(admin, s.renameTeam))
	mux.Handle("PUT /api/teams/{id}/status", s.require(admin, s.setTeamStatus))
	mux.Handle("DELETE /api/teams/{id}", s.require(admin, s.deleteTeam))

	mux.Handle("GET /api/shifts", s.require(admin, s.listShifts))
	mux.Handle("POST /api/shifts", s.require(admin, s.createShift))
	mux.Handle("PUT /api/shifts/{id}", s.require(admin, s.updateShift))
	mux.Handle("DELETE /api/shifts/{id}", s.require(admin, s.deleteShift))

	// Activity: what people worked on, limited to the people the caller may see (a user: themselves)
	mux.Handle("GET /api/activity/summary", s.require(everyone, s.activitySummary))
	mux.Handle("GET /api/activity/events", s.require(everyone, s.activityEvents))

	// Time Tracker: one record per person per day, built from activity
	mux.Handle("GET /api/timetracker/days", s.require(everyone, s.timeTrackerDays))

	// Productivity: trends for what the caller may see; rating apps and websites is for the super admin
	mux.Handle("GET /api/productivity/trends", s.require(everyone, s.productivityTrends))
	mux.Handle("GET /api/productivity/rules", s.require(admin, s.listRules))
	mux.Handle("POST /api/productivity/rules", s.require(admin, s.createRule))
	mux.Handle("PUT /api/productivity/rules", s.require(admin, s.updateRules))
	mux.Handle("PUT /api/productivity/rules/{id}", s.require(admin, s.updateRule))

	// Desktop agent: signs in once, then uses its device token
	mux.HandleFunc("POST /api/agent/register", s.registerDevice)
	mux.Handle("GET /api/agent/downloads", s.require(everyone, s.agentDownloads))
	mux.Handle("GET /api/agent/download", s.require(everyone, s.agentDownload))
	mux.HandleFunc("GET /extension/updates.xml", s.extensionUpdates)
	mux.HandleFunc("GET /extension/hrlens.crx", s.extensionPackage)
	mux.Handle("POST /api/agent/heartbeat", s.requireDevice(s.agentHeartbeat))
	mux.Handle("POST /api/agent/activity", s.requireDevice(s.agentActivity))
	mux.Handle("POST /api/agent/screenshots", s.requireDevice(s.agentScreenshot))
	mux.Handle("POST /api/agent/recordings", s.requireDevice(s.agentRecording))
	mux.Handle("POST /api/agent/live", s.requireDevice(s.agentLiveFrame))
	mux.Handle("POST /api/agent/input", s.requireDevice(s.agentInput))
	mux.Handle("POST /api/agent/presence", s.requireDevice(s.agentPresence))
	mux.Handle("GET /api/agent/update", s.requireDevice(s.agentUpdate))
	mux.Handle("GET /api/agent/update/download", s.requireDevice(s.agentUpdateDownload))

	// Monitoring: screenshots, recordings and live view of people the caller may see
	// A user sees only their own screenshots; recordings, counts and live view are for managers
	mux.Handle("GET /api/monitoring/captures", s.require(everyone, s.listCaptures))
	mux.Handle("GET /api/monitoring/captures/{id}/file", s.require(everyone, s.captureFile))
	mux.Handle("GET /api/monitoring/input", s.require(managers, s.inputLevels))
	mux.Handle("GET /api/monitoring/live", s.require(managers, s.liveStatus))
	mux.Handle("GET /api/monitoring/live/{id}/frame", s.require(managers, s.liveFrame))

	// Desktops and Emails (super admin)
	mux.Handle("GET /api/devices", s.require(admin, s.listDevices))
	mux.Handle("DELETE /api/devices/{id}", s.require(admin, s.removeDevice))
	mux.Handle("GET /api/email-reports", s.require(admin, s.listEmailReports))
	mux.Handle("POST /api/email-reports", s.require(admin, s.createEmailReport))
	mux.Handle("PUT /api/email-reports/{id}", s.require(admin, s.updateEmailReport))
	mux.Handle("PUT /api/email-reports/{id}/enabled", s.require(admin, s.setEmailReportEnabled))
	mux.Handle("DELETE /api/email-reports/{id}", s.require(admin, s.deleteEmailReport))

	mux.Handle("GET /api/lookups", s.require(admin, s.lookups))
	for _, table := range []string{"departments", "branches"} {
		mux.Handle("POST /api/"+table, s.require(admin, s.createLookup(table)))
		mux.Handle("DELETE /api/"+table+"/{id}", s.require(admin, s.deleteLookup(table)))
	}

	// In production the built Angular app is served from the same origin (no CORS); in dev
	// WEB_DIR is empty and the app runs on its own :4200.
	if s.cfg.WebDir != "" {
		mux.Handle("/", spaHandler(s.cfg.WebDir))
	}

	return s.recoverer(s.logger(s.cors(mux)))
}

// ---------- Middleware ----------

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status,
			"ms", time.Since(start).Milliseconds(), "ip", clientIP(r))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic", "error", fmt.Sprint(v), "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, "Something went wrong")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// cors lets only the configured web app addresses call the API from a browser, and
// refuses changes that come from any other site (cross-site request forgery).
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if !slices.Contains(s.cfg.CORSOrigins, origin) && !sameHost(origin, r.Host) {
				writeError(w, http.StatusForbidden, "Origin not allowed")
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Expose-Headers", "X-Frame-Time")
			h.Add("Vary", "Origin")
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE")
				h.Set("Access-Control-Allow-Headers", "Content-Type")
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func sameHost(origin, host string) bool {
	_, rest, ok := strings.Cut(origin, "://")
	return ok && rest == host
}

// ---------- Signed-in user ----------

type currentUser struct {
	ID        int64
	Role      auth.Role
	TeamID    *int64
	SessionID int64
}

type ctxKey struct{}

func userFrom(r *http.Request) currentUser {
	return r.Context().Value(ctxKey{}).(currentUser)
}

// require runs next only for a signed-in, active user holding one of roles.
func (s *Server) require(roles []auth.Role, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "Sign in to continue")
			return
		}

		var u currentUser
		var role string
		err = s.db.QueryRow(r.Context(), `
			SELECT u.id, u.role, u.team_id, s.id
			FROM sessions s JOIN users u ON u.id = s.user_id
			WHERE s.token_hash = $1 AND s.expires_at > now() AND u.status = 'active'`,
			auth.HashToken(cookie.Value)).Scan(&u.ID, &role, &u.TeamID, &u.SessionID)
		if err != nil {
			s.clearCookie(w)
			writeError(w, http.StatusUnauthorized, "Sign in to continue")
			return
		}
		u.Role = auth.Role(role)

		allowed := false
		for _, want := range roles {
			allowed = allowed || want == u.Role
		}
		if !allowed {
			writeError(w, http.StatusForbidden, "You don't have access to this")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	})
}

func (s *Server) setCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

// ---------- Helpers ----------

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

type errorBody struct {
	Error  string            `json:"error"`
	Fields map[string]string `json:"fields,omitempty"`
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorBody{Error: message})
}

func writeFieldErrors(w http.ResponseWriter, fields map[string]string) {
	writeJSON(w, http.StatusUnprocessableEntity, errorBody{Error: "Check the highlighted fields", Fields: fields})
}

// fail logs an unexpected error and answers without leaking details.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "path", r.URL.Path, "error", err)
	writeError(w, http.StatusInternalServerError, "Something went wrong")
}

// decode reads a JSON body into dst, writing the error response itself on failure.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "Not found")
		return 0, false
	}
	return id, true
}

// uniqueViolation returns the broken constraint's name, or "" if err is something else.
func uniqueViolation(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName
	}
	return ""
}

func foreignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// clientIP is the connecting address. Behind a reverse proxy this is the proxy;
// read its forwarded header there instead, once you know which proxy you trust.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
