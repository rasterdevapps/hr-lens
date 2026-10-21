# Deploying HR Lens on an office server (reachable over HTTPS)

This sets up HR Lens on an always-on office machine, served to on-site and remote employees over
HTTPS. The Go server serves both the web app and the API on one port (8080); a reverse proxy in
front terminates HTTPS.

## What you need

- A Linux office server (64-bit), always on, with a fixed LAN IP (reserve it in the router's DHCP).
- Go 1.23+ and Node 20+ on a build machine (your dev PC is fine) to build the bundle.
- PostgreSQL 16 on the server.
- A domain name you control (e.g. `hrlens.yourcompany.com`) for HTTPS.
- One way to reach the server from the internet — pick **A** or **B** in step 5.

## 1. Build the bundle (on your dev PC)

```bash
cd backend
./deploy/build-bundle.sh
```

This produces `backend/deploy/hrlens-deploy.tar.gz`. Copy it to the server:

```bash
scp backend/deploy/hrlens-deploy.tar.gz you@office-server:/tmp/
```

## 2. Install on the server

```bash
sudo useradd -r -s /usr/sbin/nologin hrlens
sudo mkdir -p /opt/hrlens-server /var/lib/hrlens/data
sudo tar -C /tmp -xzf /tmp/hrlens-deploy.tar.gz
sudo cp -r /tmp/out/. /opt/hrlens-server/
sudo chown -R hrlens:hrlens /opt/hrlens-server /var/lib/hrlens
```

## 3. PostgreSQL

```bash
sudo -u postgres psql -c "CREATE USER hrlens WITH PASSWORD 'CHANGE_ME_STRONG';"
sudo -u postgres psql -c "CREATE DATABASE hrlens OWNER hrlens;"
```

## 4. Configure and start the service

```bash
cd /opt/hrlens-server
sudo -u hrlens cp .env.example .env
sudo -u hrlens nano .env         # set DATABASE_URL password, ADMIN_EMAIL, ADMIN_PASSWORD (first run), CORS_ORIGIN=https://your-domain
sudo cp hrlens.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now hrlens
sudo systemctl status hrlens     # should be active (running)
journalctl -u hrlens -f          # watch the log; migrations run on first start
```

The server now answers on `http://<server-LAN-IP>:8080`. After the first start, remove
`ADMIN_PASSWORD` from `.env` and `sudo systemctl restart hrlens`.

## 5. HTTPS and reaching it from outside

The session cookie is `Secure` (`COOKIE_SECURE=true`), so the site must be HTTPS. Pick one:

### A. Cloudflare Tunnel (recommended — no port forwarding, works behind NAT/dynamic IP)

Your domain must be on Cloudflare (free plan is fine).

```bash
# Install cloudflared, then:
cloudflared tunnel login
cloudflared tunnel create hrlens
cloudflared tunnel route dns hrlens hrlens.yourcompany.com
# /etc/cloudflared/config.yml:
#   tunnel: <tunnel-id>
#   credentials-file: /root/.cloudflared/<tunnel-id>.json
#   ingress:
#     - hostname: hrlens.yourcompany.com
#       service: http://localhost:8080
#     - service: http_status:404
sudo cloudflared service install
```

Cloudflare provides the HTTPS certificate and forwards to the server. Nothing is opened on the
office router.

### B. Caddy + port forwarding (if you can forward ports 80/443)

Point the domain's A record at the office's public IP (add DDNS if it changes), forward ports
80 and 443 on the router to the server, then:

```bash
sudo apt install caddy
sudo cp Caddyfile /etc/caddy/Caddyfile    # edit the domain
sudo systemctl restart caddy
```

Caddy gets and renews a Let's Encrypt certificate and proxies to `:8080`.

Either way, the app is then at `https://hrlens.yourcompany.com`.

## 6. Point the agents at it

In the Download Agent dialog the install command takes the server URL. On each computer, after the
machine-wide install, each person registers against the public URL:

```bash
sudo ./install.sh https://hrlens.yourcompany.com
/opt/hrlens/hrlens-agent register -server https://hrlens.yourcompany.com -email them@company.com
```

## Updating later

Rebuild the bundle, copy it over, then:

```bash
sudo systemctl stop hrlens
sudo cp /tmp/out/hrlens-server /opt/hrlens-server/hrlens-server
sudo cp -r /tmp/out/web/. /opt/hrlens-server/web/
sudo systemctl start hrlens
```

To publish a new desktop-agent version to installed agents, run `go run ./cmd/release -version X`
on the build machine and copy `dist/agent/` into `/opt/hrlens-server/dist/agent/`.
