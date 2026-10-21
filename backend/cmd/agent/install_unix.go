//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ---------- install / uninstall: start at login ----------

const autostartEntry = `[Desktop Entry]
Type=Application
Name=HR Lens agent
Comment=Sends activity, screenshots and screen recordings to HR Lens
Exec=%s run
Terminal=false
NoDisplay=true
X-GNOME-Autostart-enabled=true
`

// A machine-wide install (run as root) puts the program where every user can start it and adds
// a system autostart entry, so the agent runs in each person's desktop session at login. Each
// person still signs this computer in to their own HR Lens account (their own config.json).
const (
	systemBinPath   = "/opt/hrlens/hrlens-agent"
	systemAutostart = "/etc/xdg/autostart/hrlens-agent.desktop"
)

func autostartPaths() (bin, entry string, err error) {
	if os.Geteuid() == 0 {
		return systemBinPath, systemAutostart, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(home, ".local", "bin", "hrlens-agent"), filepath.Join(cfg, "autostart", "hrlens-agent.desktop"), nil
}

// install copies this program into place and adds a desktop autostart entry, so the agent runs
// inside the desktop session (where it can see the screen) at every login. Run as root it
// installs for every user of this computer (/opt + /etc/xdg/autostart); run as a normal user it
// installs for that user only (~/.local/bin + ~/.config/autostart).
func install() error {
	if runtime.GOOS != "linux" {
		return errors.New("install is only available on Linux so far")
	}
	system := os.Geteuid() == 0
	// A personal install tracks the user running it, so it must already be signed in. A
	// machine-wide install does not sign anyone in: each user does that for themselves later.
	if !system {
		if _, _, err := loadConfig(); err != nil {
			return err
		}
	}
	bin, entry, err := autostartPaths()
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return err
	}
	if self != bin {
		data, err := os.ReadFile(self)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			return err
		}
		tmp := bin + ".new"
		if err := os.WriteFile(tmp, data, 0o755); err != nil {
			return err
		}
		if err := os.Rename(tmp, bin); err != nil { // replaces a running copy safely
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(entry, []byte(fmt.Sprintf(autostartEntry, bin)), 0o644); err != nil {
		return err
	}
	if system {
		fmt.Printf("Installed %s for every user of this computer; it starts in each person's session at login.\n"+
			"Each person signs this computer in to their own HR Lens account once:\n  %s register -server <HR Lens URL> -email <their email>\n", bin, bin)
	} else {
		fmt.Printf("Installed %s\nIt starts at every login (%s).\nStart it now with:  %s run &\n", bin, entry, bin)
	}
	return nil
}

func uninstall() error {
	_, entry, err := autostartPaths()
	if err != nil {
		return err
	}
	if err := os.Remove(entry); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if os.Geteuid() == 0 {
		fmt.Printf("Removed the machine-wide autostart (%s). Stop running copies with:  pkill -f 'hrlens-agent run'\n", entry)
	} else {
		fmt.Println("The agent no longer starts at login. Stop a running copy with:  pkill -f 'hrlens-agent run'")
	}
	return nil
}

// replaceFile renames src over dst; a running dst keeps its old copy open, so this is safe.
func replaceFile(src, dst string) error { return os.Rename(src, dst) }
