package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgconn"
)

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// AddMissingActivityRules creates a productivity rule for every application and website
// seen in activity_events that has none yet, rated the way most of its activity was.
// An application only ever seen with websites (a browser) becomes neutral "Browsing".
// It returns how many rules it added.
func AddMissingActivityRules(ctx context.Context, db execer) (int64, error) {
	sites, err := db.Exec(ctx, `
		INSERT INTO activity_rules (kind, name, category, productivity)
		SELECT 'website', lower(domain),
		       mode() WITHIN GROUP (ORDER BY category),
		       mode() WITHIN GROUP (ORDER BY productivity)
		FROM activity_events
		WHERE domain IS NOT NULL
		GROUP BY lower(domain)
		ON CONFLICT DO NOTHING`)
	if err != nil {
		return 0, err
	}
	apps, err := db.Exec(ctx, `
		INSERT INTO activity_rules (kind, name, category, productivity)
		SELECT 'application', min(app_name),
		       COALESCE(mode() WITHIN GROUP (ORDER BY category) FILTER (WHERE domain IS NULL), 'Browsing'),
		       COALESCE(mode() WITHIN GROUP (ORDER BY productivity) FILTER (WHERE domain IS NULL), 'neutral')
		FROM activity_events
		GROUP BY lower(app_name)
		ON CONFLICT DO NOTHING`)
	if err != nil {
		return 0, err
	}
	return sites.RowsAffected() + apps.RowsAffected(), nil
}
