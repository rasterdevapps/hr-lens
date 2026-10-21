//go:build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

// ---------- install / uninstall: start at sign-in ----------
//
// The Windows package has two copies of the agent: hrlens-agent.exe is a console program for
// register / status / install, and hrlens-agentw.exe is the same program built without a
// console window, which runs in the background after sign-in. Starting at sign-in is a value
// under the registry's ...\CurrentVersion\Run key.
//
// Run from an administrator prompt, install puts the agent in Program Files and starts it for
// everyone who signs in to this computer (HKLM); otherwise it installs for the current user
// only (%LOCALAPPDATA%\Programs, HKCU). Each person registers to their own HR Lens account.

const (
	cliExe     = "hrlens-agent.exe"
	bgExe      = "hrlens-agentw.exe"
	runValue   = "HR Lens agent"
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
)

func installDir(system bool) (string, error) {
	if system {
		pf := os.Getenv("ProgramFiles")
		if pf == "" {
			return "", errors.New("ProgramFiles is not set")
		}
		return filepath.Join(pf, "HR Lens"), nil
	}
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return "", errors.New("LOCALAPPDATA is not set")
	}
	return filepath.Join(local, "Programs", "HR Lens"), nil
}

func runKey(system bool) string {
	if system {
		return `HKLM\` + runKeyPath
	}
	return `HKCU\` + runKeyPath
}

// autostartPaths returns the background program that starts at sign-in and where that is set.
func autostartPaths() (bin, entry string, err error) {
	system := isAdmin()
	dir, err := installDir(system)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(dir, bgExe), runKey(system) + `\` + runValue, nil
}

func install() error {
	system := isAdmin()
	dir, err := installDir(system)
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	from := filepath.Dir(self)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range []string{cliExe, bgExe} {
		src := filepath.Join(from, name)
		if _, err := os.Stat(src); err != nil {
			return fmt.Errorf("%s is missing next to this program: unpack the whole HR Lens agent zip first", name)
		}
		if err := copyInto(src, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	bin := filepath.Join(dir, bgExe)
	if err := setupBrowserHost(bin); err != nil {
		return fmt.Errorf("register browser host: %w", err)
	}
	cmd := exec.Command("reg", "add", runKey(system), "/v", runValue, "/t", "REG_SZ", "/d", `"`+bin+`" run`, "/f")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("adding the sign-in entry: %v: %s", err, out)
	}
	cli := filepath.Join(dir, cliExe)
	if system {
		fmt.Printf("Installed in %s for everyone who signs in to this computer; it starts at sign-in.\n"+
			"Each person signs this computer in to their own HR Lens account once:\n  \"%s\" register -server <HR Lens URL> -email <their email>\n", dir, cli)
	} else {
		fmt.Printf("Installed in %s\nIt starts at every sign-in. Start it now with:  start \"\" \"%s\" run\n", dir, bin)
	}
	return nil
}

// copyInto copies src over dst. Windows will not overwrite a running program, but it can be
// renamed out of the way, so the old copy goes to a unique dst.old.* backup first.
func copyInto(src, dst string) error {
	if same, _ := sameFile(src, dst); same {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer os.Remove(tmp) // no-op after the rename
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return replaceFile(tmp, dst)
}

// replaceFile renames src over dst, moving a running dst aside to a unique .old.* backup
// first, ignoring locked prior backups, and rolling back if moving src into place fails.
func replaceFile(src, dst string) error {
	return replaceWithBackup(src, dst, os.Rename, os.Remove)
}

func sameFile(a, b string) (bool, error) {
	sa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	sb, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(sa, sb), nil
}

func uninstall() error {
	system := isAdmin()
	cmd := exec.Command("reg", "delete", runKey(system), "/v", runValue, "/f")
	if out, err := cmd.CombinedOutput(); err != nil {
		// Not there means already uninstalled
		if q := exec.Command("reg", "query", runKey(system), "/v", runValue); q.Run() == nil {
			return fmt.Errorf("removing the sign-in entry: %v: %s", err, out)
		}
	}
	if err := removeBrowserHost(); err != nil {
		return err
	}
	fmt.Println("The agent no longer starts at sign-in. Use its tray Quit action to stop the running copy gracefully.")
	return nil
}

// isAdmin reports whether this program runs elevated ("Run as administrator").
func isAdmin() bool {
	var token syscall.Token
	p, err := syscall.GetCurrentProcess()
	if err != nil || syscall.OpenProcessToken(p, syscall.TOKEN_QUERY, &token) != nil {
		return false
	}
	defer token.Close()
	const tokenElevation = 20
	var elevated, n uint32
	if syscall.GetTokenInformation(token, tokenElevation, (*byte)(unsafe.Pointer(&elevated)), 4, &n) != nil {
		return false
	}
	return elevated != 0
}
