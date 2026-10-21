// Command seed fills a development database with sample teams, users and activity.
// It is safe to run again: existing teams and users are left untouched.
//
//	go run ./cmd/seed
//
// Do not run it against a production database.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"hrlens/backend/internal/auth"
	"hrlens/backend/internal/config"
	"hrlens/backend/internal/db"
)

// Every sample user signs in with this password.
const samplePassword = "Welcome@123"

type team struct {
	name, department string
	members          []string // the first member is the team's manager
}

var teams = []team{
	{"Engineering", "IT", []string{"Priya Raman", "Arun Vijay", "Karthik Subramanian", "Divya Krishnan", "Naveen Kumar", "Sanjay Menon"}},
	{"Mobile Development", "IT", []string{"Meera Iyer", "Rahul Nair", "Anitha Rajan", "Vignesh Babu"}},
	{"Testing", "IT", []string{"Lakshmi Narayanan", "Deepak Chandran", "Swathi Mohan", "Harish Balaji"}},
	{"Design", "Design", []string{"Farhan Ali", "Nisha Pillai", "Gokul Anand"}},
	{"Support", "Support", []string{"Kavya Nair", "Pradeep Rao", "Riya Sharma", "Manoj Prabhu", "Sowmya Devi"}},
	{"Human Resources", "HR", []string{"Anita Joseph", "Revathi Suresh", "Balaji Venkat"}},
}

var branches = []string{"Chennai", "Coimbatore", "Bengaluru"}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed failed:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if _, err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	hash, err := auth.HashPassword(samplePassword)
	if err != nil {
		return err
	}

	added, skipped := 0, 0
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		// lookup returns the id of the named row, creating it if needed
		lookup := func(table, name string) (id int64, err error) {
			_, err = tx.Exec(ctx, `INSERT INTO `+table+` (name) SELECT $1 WHERE NOT EXISTS
				(SELECT 1 FROM `+table+` WHERE lower(name) = lower($1))`, name)
			if err != nil {
				return 0, err
			}
			err = tx.QueryRow(ctx, `SELECT id FROM `+table+` WHERE lower(name) = lower($1)`, name).Scan(&id)
			return id, err
		}

		var branchIDs []int64
		for _, b := range branches {
			id, err := lookup("branches", b)
			if err != nil {
				return err
			}
			branchIDs = append(branchIDs, id)
		}

		n := 0
		for _, t := range teams {
			teamID, err := lookup("teams", t.name)
			if err != nil {
				return err
			}
			deptID, err := lookup("departments", t.department)
			if err != nil {
				return err
			}

			var managerID *int64
			for i, name := range t.members {
				n++
				first, last, _ := strings.Cut(name, " ")
				email := strings.ToLower(first+"."+last) + "@example.com"
				role := auth.User
				if i == 0 {
					role = auth.Manager
				}

				// A spread of "last active" times; every sixth person has never signed in
				var lastActive *time.Time
				if n%6 != 0 {
					at := time.Now().Add(-time.Duration(n*n*37) * time.Second)
					lastActive = &at
				}

				var id int64
				err := tx.QueryRow(ctx, `
					INSERT INTO users (employee_code, first_name, last_name, email, mobile, password_hash, role, status,
					                   team_id, department_id, branch_id, manager_id, last_active_at)
					SELECT $1, $2, $3, $4, $5, $6, $7::user_role, $8::user_status, $9, $10, $11, $12, $13
					WHERE NOT EXISTS (SELECT 1 FROM users WHERE lower(email) = lower($4) OR lower(employee_code) = lower($1))
					RETURNING id`,
					fmt.Sprintf("EMP%03d", n), first, last, email, fmt.Sprintf("98400%05d", 10000+n*137), hash,
					string(role), status(n), teamID, deptID, branchIDs[n%len(branchIDs)], managerID, lastActive,
				).Scan(&id)
				switch {
				case err == pgx.ErrNoRows:
					skipped++
					if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE lower(email) = lower($1)`, email).Scan(&id); err != nil {
						id = 0 // the employee code is taken by someone else
					}
				case err != nil:
					return err
				default:
					added++
				}

				if i == 0 && id != 0 {
					managerID = &id
					// A manager administers their own team
					if _, err := tx.Exec(ctx, `INSERT INTO user_admin_groups (user_id, team_id) VALUES ($1, $2)
						ON CONFLICT DO NOTHING`, id, teamID); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	fmt.Printf("Added %d users (%d already existed), in %d teams.\n", added, skipped, len(teams))
	if added > 0 {
		fmt.Printf("They sign in with their email (first.last@example.com) and the password %s\n", samplePassword)
	}
	return seedActivity(ctx, pool)
}

// status makes two of the sample users inactive, so the status filter has something to show.
// Both are ordinary users: every team's manager stays active.
func status(n int) string {
	if n == 13 || n == 19 {
		return "inactive"
	}
	return "active"
}
