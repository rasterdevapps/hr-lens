#!/usr/bin/env bash
# Installs the HR Lens desktop agent for every user of this computer (machine-wide).
#
#   sudo ./install.sh                         # asks for the server address
#   sudo ./install.sh https://hr.example.com
#
# Run it with sudo, once per computer. It installs the program system-wide, makes it start in
# each person's desktop session at login, installs the missing packages, and adds the browser
# extension policy. Each person then signs this computer in to their own HR Lens account:
#
#   /opt/hrlens/hrlens-agent register -server https://hr.example.com -email you@company.com
#
set -euo pipefail

DEFAULT_SERVER=""
SERVER="${1:-}"
HERE="$(cd "$(dirname "$0")" && pwd)"
BIN="/opt/hrlens/hrlens-agent"

say()  { printf '\n\033[1m%s\033[0m\n' "$*"; }
fail() { printf '\n\033[31mError:\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "run this with sudo:  sudo ./install.sh"
[ "$(uname -m)" = "x86_64" ] || fail "this package is for 64-bit Intel/AMD computers (this one is $(uname -m))."
[ -x "$HERE/hrlens-agent" ] || fail "hrlens-agent is missing next to install.sh."

if [ -z "$SERVER" ]; then
  read -r -p "HR Lens server [$DEFAULT_SERVER]: " SERVER
  SERVER="${SERVER:-$DEFAULT_SERVER}"
fi
SERVER="${SERVER%/}"
[ -n "$SERVER" ] || fail "the server address is required"

say "1/4  Checking required packages"
missing=()
command -v gst-launch-1.0 >/dev/null || missing+=(gstreamer1.0-tools)
gst-inspect-1.0 vp8enc  >/dev/null 2>&1 || missing+=(gstreamer1.0-plugins-good)
gst-inspect-1.0 videoscale >/dev/null 2>&1 || missing+=(gstreamer1.0-plugins-base)
command -v xprop  >/dev/null || missing+=(x11-utils)
command -v xinput >/dev/null || missing+=(xinput)
if [ ${#missing[@]} -gt 0 ]; then
  echo "Installing: ${missing[*]}"
  apt-get update -qq
  apt-get install -y "${missing[@]}"
else
  echo "All present."
fi

say "2/4  Installing the program for every user"
install -D -m 0755 "$HERE/hrlens-agent" "$BIN"
# Writes /etc/xdg/autostart/hrlens-agent.desktop so it starts in each person's session at login
"$BIN" install

say "3/4  Adding the HR Lens extension to web browsers"
# The extension tells the agent which website the front tab shows (site name only).
EXT_ID="jobmeopcpnhamneamceglchglicfhdlk"
POLICY="{\"ExtensionInstallForcelist\": [\"$EXT_ID;$SERVER/extension/updates.xml\"]}"
added=()
add_policy() {  # $1 = browser command, $2 = policy folder, $3 = name
  command -v "$1" >/dev/null || return 0
  mkdir -p "$2"
  echo "$POLICY" > "$2/hrlens.json"
  added+=("$3")
}
add_policy google-chrome          /etc/opt/chrome/policies/managed       "Google Chrome"
add_policy chromium               /etc/chromium/policies/managed         "Chromium"
add_policy chromium-browser       /etc/chromium-browser/policies/managed "Chromium"
add_policy brave-browser          /etc/brave/policies/managed            "Brave"
add_policy microsoft-edge         /etc/opt/edge/policies/managed         "Microsoft Edge"
if [ ${#added[@]} -gt 0 ]; then
  echo "Added for: ${added[*]}. Close and reopen the browser to load it."
else
  echo "No Chrome, Chromium, Brave or Edge found: websites will not be recorded."
fi
command -v firefox >/dev/null && echo "Note: Firefox is not supported yet; time in Firefox is recorded without the website."

say "4/4  Done"
cat <<EOF
The agent is installed for every user of this computer and starts at each login.

Now each person signs this computer in to their own HR Lens account, once, in a terminal
on their own desktop (not over SSH):

    $BIN register -server $SERVER -email their-email@company.com
    $BIN run &        # start now, without logging out

Screen capture needs an X11 (Xorg) session; it does nothing in a Wayland session.
EOF
