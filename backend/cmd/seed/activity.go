package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hrlens/backend/internal/db"
)

// Sample activity until the desktop agent sends real data.

const activityDays = 35 // how far back to fill, counting today

// source is one application, or one website in a browser
type source struct {
	app, appName, domain, category, productivity string
	titles                                       []string
}

func web(domain, category, productivity string, titles ...string) source {
	return source{"chrome.exe", "Google Chrome", domain, category, productivity, titles}
}

func app(exe, name, category, productivity string, titles ...string) source {
	return source{exe, name, "", category, productivity, titles}
}

var sources = map[string]source{
	// Websites
	"github":    web("github.com", "Development", "productive", "hrlens/backend: Pull request #42 · GitHub", "Issues · hrlens/screen-management", "Actions · build #318 · GitHub"),
	"so":        web("stackoverflow.com", "Development", "productive", "How to use pgx CopyFrom - Stack Overflow", "Angular signals with forms - Stack Overflow"),
	"angular":   web("angular.dev", "Development", "productive", "Signals • Overview • Angular", "Router • Angular"),
	"godev":     web("pkg.go.dev", "Development", "productive", "net/http - Go Packages", "pgxpool package - pkg.go.dev"),
	"jira":      web("raster.atlassian.net", "Project Management", "productive", "HRL-128 Activity pages - Jira", "Sprint 14 board - Jira"),
	"gmail":     web("mail.google.com", "Communication", "neutral", "Inbox (12) - Gmail", "Weekly report - Gmail"),
	"meet":      web("meet.google.com", "Meetings", "productive", "Meet - Daily stand-up", "Meet - Sprint review"),
	"gdocs":     web("docs.google.com", "Office", "productive", "Release notes - Google Docs", "Leave tracker - Google Sheets"),
	"chatgpt":   web("chatgpt.com", "Research", "productive", "ChatGPT", "ChatGPT - SQL window functions"),
	"figmaweb":  web("figma.com", "Design", "productive", "HR Lens - Dashboard – Figma"),
	"zendesk":   web("raster.zendesk.com", "Customer Support", "productive", "Ticket #4821 - Zendesk", "Views - Zendesk"),
	"linkedin":  web("linkedin.com", "Social Media", "neutral", "Feed | LinkedIn", "Messaging | LinkedIn"),
	"naukri":    web("naukri.com", "Recruitment", "productive", "Resdex - Naukri.com"),
	"youtube":   web("youtube.com", "Entertainment", "unproductive", "Lo-fi beats to code to - YouTube", "Cricket highlights - YouTube"),
	"facebook":  web("facebook.com", "Social Media", "unproductive", "Facebook"),
	"news":      web("news.google.com", "News", "unproductive", "Google News - Top stories"),
	"amazon":    web("amazon.in", "Shopping", "unproductive", "Amazon.in: Online Shopping"),
	"localhost": web("localhost", "Development", "productive", "HR Lens - Dashboard", "HR Lens - Activity"),

	// Applications
	"code":     app("Code.exe", "Visual Studio Code", "Development", "productive", "activity.go - backend - Visual Studio Code", "shell.component.ts - screen-management - Visual Studio Code"),
	"studio":   app("studio64.exe", "Android Studio", "Development", "productive", "MainActivity.kt - hrlens-mobile"),
	"pgadmin":  app("pgAdmin4.exe", "pgAdmin 4", "Development", "productive", "pgAdmin 4"),
	"postman":  app("Postman.exe", "Postman", "Development", "productive", "GET /api/activity/summary - Postman"),
	"terminal": app("WindowsTerminal.exe", "Windows Terminal", "Development", "productive", "go run ./cmd/server"),
	"slack":    app("slack.exe", "Slack", "Communication", "neutral", "#dev - Raster - Slack", "Priya Raman (DM) - Slack"),
	"outlook":  app("OUTLOOK.EXE", "Microsoft Outlook", "Communication", "neutral", "Inbox - Outlook", "Calendar - Outlook"),
	"teams":    app("ms-teams.exe", "Microsoft Teams", "Meetings", "productive", "Meeting | Microsoft Teams"),
	"excel":    app("EXCEL.EXE", "Microsoft Excel", "Office", "productive", "Payroll October.xlsx - Excel", "Attendance report.xlsx - Excel"),
	"word":     app("WINWORD.EXE", "Microsoft Word", "Office", "productive", "Offer letter.docx - Word", "Policy handbook.docx - Word"),
	"figma":    app("Figma.exe", "Figma", "Design", "productive", "HR Lens - Activity pages", "Design system – Figma"),
	"ps":       app("Photoshop.exe", "Adobe Photoshop", "Design", "productive", "banner.psd @ 66%"),
	"telegram": app("Telegram.exe", "Telegram", "Social Media", "unproductive", "Telegram"),
	"spotify":  app("Spotify.exe", "Spotify", "Entertainment", "unproductive", "Spotify Premium"),
	"explorer": app("explorer.exe", "File Explorer", "Utilities", "neutral", "Downloads", "Documents"),
}

// What each team mostly uses, as weights. Everyone also gets some time-wasters.
var profiles = map[string]map[string]int{
	"Engineering":        {"code": 30, "github": 10, "so": 6, "angular": 4, "godev": 4, "terminal": 6, "pgadmin": 5, "postman": 4, "localhost": 6, "jira": 4, "slack": 6, "chatgpt": 4, "meet": 3},
	"Mobile Development": {"studio": 30, "code": 8, "github": 8, "so": 6, "postman": 4, "jira": 5, "slack": 6, "chatgpt": 4, "meet": 3},
	"Testing":            {"postman": 18, "localhost": 16, "jira": 14, "excel": 6, "github": 5, "slack": 6, "teams": 4, "gdocs": 5},
	"Design":             {"figma": 30, "figmaweb": 8, "ps": 12, "slack": 6, "jira": 4, "meet": 4, "localhost": 4},
	"Support":            {"zendesk": 30, "outlook": 12, "gmail": 8, "teams": 8, "excel": 4, "slack": 6, "gdocs": 4},
	"Human Resources":    {"excel": 18, "word": 14, "outlook": 14, "gmail": 8, "linkedin": 8, "naukri": 8, "teams": 6, "gdocs": 6},
	"":                   {"outlook": 12, "excel": 10, "gmail": 8, "teams": 8, "gdocs": 8, "slack": 6, "localhost": 6, "chatgpt": 4},
}

var distractions = map[string]int{"youtube": 3, "facebook": 2, "news": 2, "amazon": 1, "telegram": 2, "spotify": 1, "explorer": 2}

type seedUser struct {
	id   int64
	team string
	tz   string
}

// seedActivity fills activity for every active user who has none yet.
func seedActivity(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, `
		SELECT u.id, COALESCE(t.name, ''), u.time_zone
		FROM users u LEFT JOIN teams t ON t.id = u.team_id
		WHERE u.status = 'active'
		  AND NOT EXISTS (SELECT 1 FROM activity_events e WHERE e.user_id = u.id)
		ORDER BY u.id`)
	if err != nil {
		return err
	}
	users, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (seedUser, error) {
		var u seedUser
		return u, r.Scan(&u.id, &u.team, &u.tz)
	})
	if err != nil {
		return err
	}

	total := 0
	for _, u := range users {
		events := activityFor(u, time.Now())
		n, err := pool.CopyFrom(ctx, pgx.Identifier{"activity_events"},
			[]string{"user_id", "started_at", "duration_sec", "app", "app_name", "title", "domain", "category", "productivity"},
			pgx.CopyFromRows(events))
		if err != nil {
			return fmt.Errorf("activity for user %d: %w", u.id, err)
		}
		total += int(n)
	}
	fmt.Printf("Added %d sample activity rows for %d people.\n", total, len(users))

	rules, err := db.AddMissingActivityRules(ctx, pool)
	if err != nil {
		return fmt.Errorf("productivity rules: %w", err)
	}
	fmt.Printf("Added %d productivity rules.\n", rules)
	return nil
}

// activityFor makes a working day's worth of activity for each recent weekday, up to now.
func activityFor(u seedUser, now time.Time) [][]any {
	loc, err := time.LoadLocation(u.tz)
	if err != nil {
		loc, _ = time.LoadLocation("Asia/Kolkata")
	}
	weights := map[string]int{}
	profile, ok := profiles[u.team]
	if !ok {
		profile = profiles[""]
	}
	for k, w := range profile {
		weights[k] = w
	}
	for k, w := range distractions {
		weights[k] += w
	}
	keys, sum := make([]string, 0, len(weights)), 0
	for k, w := range weights {
		keys = append(keys, k)
		sum += w
	}
	slices.Sort(keys) // same output on every run

	var out [][]any
	today := now.In(loc)
	for back := activityDays - 1; back >= 0; back-- {
		day := time.Date(today.Year(), today.Month(), today.Day()-back, 0, 0, 0, 0, loc)
		if wd := day.Weekday(); wd == time.Sunday || (wd == time.Saturday && day.Day()%3 != 0) {
			continue
		}
		rnd := rand.New(rand.NewPCG(uint64(u.id), uint64(day.Unix())))
		if rnd.IntN(20) == 0 {
			continue // a day off now and then
		}
		start := day.Add(9*time.Hour + time.Duration(rnd.IntN(75))*time.Minute)
		end := start.Add(7*time.Hour + 30*time.Minute + time.Duration(rnd.IntN(120))*time.Minute)
		lunch := day.Add(13 * time.Hour)

		for t := start; t.Before(end) && t.Before(now); {
			if !t.Before(lunch) && t.Before(lunch.Add(45*time.Minute)) {
				t = lunch.Add(45 * time.Minute)
				continue
			}
			// Mostly short stretches, sometimes a long focused one
			secs := 20 + rnd.IntN(300)
			if rnd.IntN(4) == 0 {
				secs += rnd.IntN(1500)
			}
			pick := rnd.IntN(sum)
			var src source
			for _, k := range keys {
				if pick -= weights[k]; pick < 0 {
					src = sources[k]
					break
				}
			}
			var domain any
			if src.domain != "" {
				domain = src.domain
			}
			out = append(out, []any{u.id, t, secs, src.app, src.appName, src.titles[rnd.IntN(len(src.titles))],
				domain, src.category, src.productivity})
			// A short gap (idle or untracked) between stretches
			t = t.Add(time.Duration(secs+rnd.IntN(45)) * time.Second)
		}
	}
	return out
}
