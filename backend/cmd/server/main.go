// Command server runs the HR Lens API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hrlens/backend/internal/api"
	"hrlens/backend/internal/auth"
	"hrlens/backend/internal/config"
	"hrlens/backend/internal/db"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	setPassword := flag.String("set-password", "", "set the password of the user with this `email` to ADMIN_PASSWORD, then exit")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	applied, err := db.Migrate(ctx, pool)
	if err != nil {
		return err
	}
	for _, v := range applied {
		log.Info("migration applied", "version", v)
	}
	if *setPassword != "" {
		return resetPassword(ctx, pool, *setPassword, cfg.AdminPassword)
	}
	if err := ensureAdmin(ctx, pool, cfg, log); err != nil {
		return err
	}

	app := api.New(cfg, pool, log)
	go app.RunMaintenance(ctx) // deletes old screenshots and recordings

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	failed := make(chan error, 1)
	go func() { failed <- srv.ListenAndServe() }()
	log.Info("listening", "addr", cfg.HTTPAddr, "allowed_origins", strings.Join(cfg.CORSOrigins, ", "))

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ensureAdmin creates the first super admin from ADMIN_EMAIL / ADMIN_PASSWORD
// while the users table is empty. It never touches existing users.
func ensureAdmin(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, log *slog.Logger) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if cfg.AdminEmail == "" || cfg.AdminPassword == "" {
		log.Warn("no users exist: set ADMIN_EMAIL and ADMIN_PASSWORD and restart to create the first super admin")
		return nil
	}
	hash, err := auth.HashPassword(cfg.AdminPassword)
	if err != nil {
		return errors.New("ADMIN_PASSWORD: " + err.Error())
	}
	email := strings.TrimSpace(cfg.AdminEmail)
	_, err = pool.Exec(ctx, `
		INSERT INTO users (employee_code, first_name, last_name, email, password_hash, role)
		VALUES ('admin', 'Super', 'Admin', $1, $2, 'super_admin')`, email, hash)
	if err != nil {
		return err
	}
	log.Info("first super admin created", "email", email)
	return nil
}

// resetPassword is the way back in for a locked-out user:
//
//	ADMIN_PASSWORD='new password' go run ./cmd/server -set-password admin@example.com
//
// It also signs that user out everywhere.
func resetPassword(ctx context.Context, pool *pgxpool.Pool, email, password string) error {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return errors.New("ADMIN_PASSWORD: " + err.Error())
	}
	tag, err := pool.Exec(ctx, `
		WITH gone AS (DELETE FROM sessions WHERE user_id = (SELECT id FROM users WHERE lower(email) = lower($1)))
		UPDATE users SET password_hash = $2, updated_at = now() WHERE lower(email) = lower($1)`,
		strings.TrimSpace(email), hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("no user with email %s", email)
	}
	fmt.Println("Password changed for", email)
	return nil
}
