# HR Lens backend

Go API with PostgreSQL. Stage 1: sign-in, the three roles, users, teams.

## Requirements

- Go 1.23 or newer
- PostgreSQL 14 or newer (or Docker, to use `docker-compose.yml`)

## Run locally

```bash
docker compose up -d          # starts PostgreSQL on localhost:5432
cp .env.example .env          # then edit ADMIN_EMAIL and ADMIN_PASSWORD
go run ./cmd/server
```

On start the server applies new database migrations and, while no users exist,
creates the first super admin from `ADMIN_EMAIL` / `ADMIN_PASSWORD`.
Remove `ADMIN_PASSWORD` from `.env` after the first start.

```bash
go test ./...                 # unit tests
go build -o bin/server ./cmd/server
```

## Locked out?

`ADMIN_EMAIL` / `ADMIN_PASSWORD` are used only once, while there are no users.
To change an existing user's password from the command line:

```bash
ADMIN_PASSWORD='new password, 8+ characters' go run ./cmd/server -set-password admin@example.com
```

## Settings

| Variable | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | required | PostgreSQL connection string |
| `HTTP_ADDR` | `:8080` | Address to listen on |
| `CORS_ORIGIN` | `http://localhost:4200` | The web app's address, or several separated by commas; the only browser origins allowed |
| `SESSION_TTL_HOURS` | `12` | How long a sign-in lasts |
| `COOKIE_SECURE` | `false` | Set `true` in production (needs HTTPS) |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD` | empty | First super admin, used only while there are no users |

## Sign-in

`POST /api/auth/login` sets an HttpOnly cookie (`hrlens_session`). The browser sends it
automatically; from Angular, call the API with `withCredentials: true`.
Five failed sign-ins for the same address and email block further tries for 15 minutes.

## Endpoints

| Method and path | Who | What |
|---|---|---|
| `GET /healthz` | anyone | Server and database check |
| `POST /api/auth/login` | anyone | Body `{email, password}`; returns the user |
| `POST /api/auth/logout` | signed in | Ends the session |
| `GET /api/auth/me` | signed in | The signed-in user |
| `POST /api/auth/password` | signed in | Body `{currentPassword, newPassword}`; signs out other devices |
| `GET /api/people` | signed in | Employees / Teams panel, limited by role |
| `GET /api/teams` | super admin, manager | Teams with member counts; query `status=active\|inactive` |
| `PUT /api/teams/{id}/status` | super admin | Body `{status}`; archives or restores a team |
| `POST /api/teams`, `PUT /api/teams/{id}`, `DELETE /api/teams/{id}` | super admin | Body `{name}` |
| `GET /api/users` | super admin | Query: `status`, `search`, `sort`, `dir`, `page`, `pageSize` |
| `POST /api/users` | super admin | Create; body as below plus `password` |
| `GET /api/users/{id}`, `PUT /api/users/{id}` | super admin | Read, update |
| `PUT /api/users/{id}/status` | super admin | Body `{status: "active" \| "inactive"}` |
| `PUT /api/users/{id}/password` | super admin | Body `{password}`; signs the user out |
| `GET /api/lookups` | super admin | Roles, teams, departments, branches, managers for the form |
| `POST /api/departments`, `DELETE /api/departments/{id}` | super admin | Body `{name}` |
| `GET /api/activity/summary` | signed in (a user: themselves) | Websites, applications or categories with time and share; see below |
| `GET /api/activity/events` | signed in (a user: themselves) | Detailed activity, newest first; see below |
| `POST /api/agent/register` | anyone | Desktop agent sign-in: body `{email, password, deviceName, os, agentVersion}`; returns a device token |
| `POST /api/agent/heartbeat`, `/activity`, `/screenshots`, `/recordings`, `/live`, `/input`, `/presence` | device token | Uploads from the desktop agent (see below); `/presence` is sign-in, lock, unlock and sign-out times |
| `GET /api/monitoring/captures` | signed in | Screenshots or recordings: `kind=screenshot\|recording` plus the activity filters. A user gets only their own screenshots |
| `GET /api/monitoring/captures/{id}/file` | signed in (a user: own screenshots) | The image or video (supports Range, so videos can be seeked) |
| `GET /api/monitoring/live?users=` | super admin, manager | Who can be watched; asking makes their agents send live frames |
| `GET /api/monitoring/live/{id}/frame` | super admin, manager | Newest live frame (JPEG), or 204 while waiting |
| `GET /api/timetracker/days` | signed in (a user: themselves) | Time Tracker: per person per day, first/last activity, working, productive, non-productive, neutral and away seconds, top apps and the day's timeline (pauses over 2 minutes are away) |
| `GET /api/productivity/trends` | signed in | Totals, Productivity Bar (5-minute slots for a day, else per day), breakdown, categories, top apps, and daily timelines for one person; activity filters |
| `GET /api/productivity/rules` | super admin | How each app or website is rated; query `kind=application\|website`, `search`, `productivity`, `category`, `sort=time\|hits\|name`, `page`, `pageSize` |
| `POST /api/productivity/rules` | super admin | Body `{kind, name, category, productivity}` |
| `PUT /api/productivity/rules/{id}` | super admin | Body `{category, productivity}` |
| `PUT /api/productivity/rules` | super admin | Body `{ids, productivity, category?}`: several at once |
| `POST /api/branches`, `DELETE /api/branches/{id}` | super admin | Body `{name}` |

User body:

```json
{
  "employeeCode": "KarthikeyanS", "firstName": "Karthikeyan", "lastName": "S",
  "email": "karthikeyan.s@raster.in", "mobile": "", "role": "manager",
  "teamId": 1, "departmentId": 1, "branchId": 1, "managerId": 2,
  "timeZone": "Asia/Kolkata", "trackAttendance": true, "adminGroups": [1]
}
```

`sort` is one of `id`, `name`, `username`, `role`, `manager`, `team`, `lastActive`.

Errors are `{"error": "message"}`, with `"fields": {"email": "..."}` for form problems.
Status codes: 400 bad request, 401 not signed in, 403 not allowed, 404 not found,
409 duplicate, 422 invalid values, 429 too many sign-in attempts.

## Who sees whom

- Super admin: everyone.
- Manager: their own team, the teams in their Admin Groups, and their direct reports.
- User: only themselves.

## Layout

```
cmd/server/                 entry point
internal/config/            settings
internal/db/                connection pool, migration runner
internal/db/migrations/     SQL files, applied in name order; add new ones, never edit applied ones
internal/auth/              roles, password hashing, session tokens, sign-in limiter
internal/api/               routes and handlers
```

## Activity

Both activity endpoints take the same filters and return only people the caller may see:

| Query | Meaning |
|---|---|
| `from`, `to` | Required. Days `YYYY-MM-DD`, inclusive, at most 366 days |
| `tz` | Time zone the days are counted in, default `Asia/Kolkata` |
| `users` | Comma-separated user ids; default everyone visible |
| `productivity` | `productive`, `neutral` or `unproductive`; default all |
| `search` | Text to look for |

`/summary` also needs `group=website|application|category` and returns
`{totals: {seconds, productive, neutral, unproductive}, items: [{name, detail, category, productivity, seconds, percent}]}`.
Each row takes the productivity level and category it spent the most time in.
`/events` also takes `page`, `pageSize` (up to 200) and `dir=asc|desc`, and returns `{total, items}`.

Rows live in `activity_events`. The desktop agent will send them; until it exists,
`go run ./cmd/seed` adds about five weeks of sample activity for every active user who has none.

## Productivity rules

`activity_rules` rates every application and website (Productivity > Configure Apps).
A browser window is rated by its website's rule; an application's rule covers its windows
with no website. Saving a rule re-rates the matching `activity_events` rows, so every page
reflects it, past activity included. `db.AddMissingActivityRules` creates a rule for any
app or website seen without one (the seed command calls it; the agent upload will too).

Trends count *away* time as the gaps between a person's first and last activity each day,
and expect 8 hours per person per weekday.

Integration tests run against the development database in `.env` and put back what they change:

```bash
go test -tags integration ./internal/api -run Integration -v
```

## Desktop agent

`cmd/agent` is the program that runs on each employee's computer. It sends:

- **activity**: the front application and window title, checked every 5 seconds, sent every minute
- **a screenshot** every 5 minutes (the server sets the interval)
- **continuous screen recording** at 1 frame per second, uploaded in 10-minute WebM pieces (about 9 MB each, ~430 MB per 8-hour day)
- **sign-in, lock, unlock and sign-out times**: the Time Tracker's start time is the day's first sign-in or unlock
- **live frames** every 2 seconds, only while a manager has that person's Live Screen open; these are never stored

Nothing is captured while the screen is locked. Files wait in a spool folder until they upload,
so nothing is lost while the server is unreachable. Screenshots and recordings are saved under
`DATA_DIR` and deleted after `RETENTION_DAYS` (default 30) by the server.

So far it supports **Linux with an X11 session** (Cinnamon, GNOME on Xorg, MATE, XFCE). Wayland
does not allow silent screen capture. It needs `gst-launch-1.0` (GStreamer, with the good plugins
for VP8/WebM) and `xprop`:

```bash
sudo apt install gstreamer1.0-tools gstreamer1.0-plugins-good x11-utils
```

Build it, sign the computer in, and make it start at every login:

```bash
go build -o bin/hrlens-agent ./cmd/agent
./bin/hrlens-agent register -server http://localhost:8080 -email you@example.com   # asks for your password
./bin/hrlens-agent install          # copies itself to ~/.local/bin and starts at every login
~/.local/bin/hrlens-agent run &     # start it now, without logging out
```

For other employees' computers, build the self-contained package (any 64-bit Linux, no Go needed there)
with the release tool. It also publishes that version to installed agents:

```bash
go run ./cmd/release -genkey            # once: creates agent-release.key and cmd/agent/updatekey.go
go run ./cmd/release -version 0.2.2     # each release: a version higher than the last
```

This writes `dist/hrlens-agent-linux.tar.gz` (the install package) and `dist/agent/linux-amd64/`
(the signed program the server hands to agents). Installed agents check about once an hour
(`GET /api/agent/update`) and install a newer version only if it is signed with the release key,
whose public half is built into them. Keep a backup of `agent-release.key` and never put it on
employee computers: without it no update can be published, and a new key means reinstalling every
agent by hand. A plain `go build` is version `dev` and never updates itself; only the installed
copy (`~/.local/bin/hrlens-agent`) does. Each computer's version is kept in `devices.agent_version`.

`hrlens-agent run` is a small watchdog that runs the capturing agent as a second process and starts
it again if it crashes (after 5 seconds, then waiting longer after each crash in a row, up to 5 minutes).

Each employee unpacks it and runs `./install.sh` in their own desktop session: it installs missing packages,
registers their account, and starts the agent now and at every login. The server's port 8080 must be
reachable from their computer (`sudo ufw allow 8080/tcp` on the server, if its firewall is on).

`hrlens-agent status` shows which account the computer uses; `hrlens-agent uninstall` stops it starting
at login. Registering a computer removes that person's seed sample activity, so their pages show only
real data from then on. `HRLENS_RECORDING_SECONDS=20` shortens recording pieces for testing.

### Websites (browser extension)

Browsers do not show the page address to other programs, so the agent learns the website from
the **HR Lens browser extension** (`browser-extension/chromium`, for Chrome, Chromium, Brave and Edge).
It sends the front tab's title and site name (e.g. `github.com`, never the full address) to the agent
at `127.0.0.1:47123`; the agent matches browser window titles to it. Without the extension, browser
time is recorded under the browser's name only. Firefox is not supported yet (it only installs
extensions signed by Mozilla).

`install.sh` adds a browser policy (`ExtensionInstallForcelist`) pointing at this server's
`/extension/updates.xml`, so the browser installs the extension and keeps it. To publish a new version,
raise `version` in its `manifest.json` and pack it with the same key (`browser-extension/key.pem`;
keep it safe: a different key makes a different extension):

```bash
cp -r browser-extension/chromium /tmp/hrlens-ext
google-chrome --pack-extension=/tmp/hrlens-ext --pack-extension-key=$PWD/browser-extension/key.pem
cp /tmp/hrlens-ext.crx dist/extension/hrlens.crx   # and update the version in dist/extension/info.json
```

Before using it with employees, tell them in writing what is captured and why: screen captures are
personal data (India's DPDP Act, GDPR and similar laws).

