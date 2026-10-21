// Package config reads settings from the environment (and an optional .env file).
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL   string
	HTTPAddr      string
	CORSOrigins   []string // browser origins allowed to call the API
	SessionTTL    time.Duration
	CookieSecure  bool
	AdminEmail    string
	AdminPassword string
	DataDir       string // screenshots and recordings from the desktop agent
	RetentionDays int    // captures older than this are deleted; 0 keeps them forever
	ExtensionDir  string // the packed browser extension (hrlens.crx, info.json)
	AgentDir      string // signed desktop agent releases, one folder per platform (see cmd/release)
	WebDir        string // built Angular app to serve (empty in dev: the app runs on its own :4200)
}

func Load() (Config, error) {
	loadDotEnv(".env")

	ttlHours, err := strconv.Atoi(env("SESSION_TTL_HOURS", "12"))
	if err != nil || ttlHours <= 0 {
		return Config{}, errors.New("SESSION_TTL_HOURS must be a positive number")
	}
	secure, err := strconv.ParseBool(env("COOKIE_SECURE", "false"))
	if err != nil {
		return Config{}, errors.New("COOKIE_SECURE must be true or false")
	}

	retention, err := strconv.Atoi(env("RETENTION_DAYS", "30"))
	if err != nil || retention < 0 {
		return Config{}, errors.New("RETENTION_DAYS must be 0 or more")
	}

	cfg := Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		HTTPAddr:      env("HTTP_ADDR", ":8080"),
		CORSOrigins:   origins(env("CORS_ORIGIN", "http://localhost:4200")),
		SessionTTL:    time.Duration(ttlHours) * time.Hour,
		CookieSecure:  secure,
		AdminEmail:    os.Getenv("ADMIN_EMAIL"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
		DataDir:       env("DATA_DIR", "data"),
		RetentionDays: retention,
		ExtensionDir:  env("EXTENSION_DIR", "dist/extension"),
		AgentDir:      env("AGENT_DIR", "dist/agent"),
		WebDir:        os.Getenv("WEB_DIR"),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	return cfg, nil
}

// origins splits a comma-separated list such as "http://localhost:4200, https://app.example.com".
func origins(list string) []string {
	var out []string
	for _, o := range strings.Split(list, ",") {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			out = append(out, o)
		}
	}
	return out
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// loadDotEnv sets variables from a KEY=VALUE file, without overriding ones already set.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			fmt.Fprintf(os.Stderr, ".env line %d ignored: expected KEY=VALUE\n", line)
			continue
		}
		key, value = strings.TrimSpace(key), strings.Trim(strings.TrimSpace(value), `"'`)
		if _, set := os.LookupEnv(key); !set {
			os.Setenv(key, value)
		}
	}
}
