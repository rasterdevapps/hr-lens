// Command release builds and signs a desktop agent version. Installed agents pick it up
// from the server within about an hour.
//
//	go run ./cmd/release -genkey            once: creates the release key (keep agent-release.key safe)
//	go run ./cmd/release -version 0.2.0     builds, signs and publishes version 0.2.0
//
// It writes, for each platform, dist/agent/<os>-<arch>/hrlens-agent and info.json (served to
// agents by the server), and the install packages dist/hrlens-agent-linux.tar.gz and
// dist/hrlens-agent-windows.zip. On Windows the self-updating program is the background copy
// (hrlens-agentw.exe, no console window); the package also has the console copy for sign-in.
// Run it from the backend folder.
package main

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

const (
	keyFile    = "agent-release.key"      // private key: never share it or put it on employee computers
	pubKeyFile = "cmd/agent/updatekey.go" // public key, built into the agent
	agentDir   = "dist/agent"
)

// Platforms the agent can capture on so far
var platforms = []struct{ goos, goarch string }{{"linux", "amd64"}, {"windows", "amd64"}}

var versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func main() {
	genkey := flag.Bool("genkey", false, "create the release key pair (once)")
	version := flag.String("version", "", "version to build, e.g. 0.2.0 (higher than the last one)")
	flag.Parse()

	var err error
	switch {
	case *genkey:
		err = generateKey()
	case versionPattern.MatchString(*version):
		err = build(*version)
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func generateKey() error {
	if _, err := os.Stat(keyFile); err == nil {
		return fmt.Errorf("%s already exists; a new key would stop installed agents from accepting updates", keyFile)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyFile, []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
		return err
	}
	src := fmt.Sprintf(`package main

// updateKey is the release public key (ed25519, base64). Written by: go run ./cmd/release -genkey
// Empty means this build cannot update itself.
const updateKey = %q
`, base64.StdEncoding.EncodeToString(pub))
	if err := os.WriteFile(pubKeyFile, []byte(src), 0o644); err != nil {
		return err
	}
	fmt.Printf("Created %s (private: keep a backup somewhere safe) and %s.\n", keyFile, pubKeyFile)
	return nil
}

func loadKey() (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(keyFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s not found: run  go run ./cmd/release -genkey  first", keyFile)
	}
	if err != nil {
		return nil, err
	}
	seed, err := base64.StdEncoding.DecodeString(string(trimNewline(data)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%s is not a release key", keyFile)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func build(version string) error {
	key, err := loadKey()
	if err != nil {
		return err
	}
	for _, p := range platforms {
		platform := p.goos + "-" + p.goarch
		dir := filepath.Join(agentDir, platform)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := refuseOlder(dir, version); err != nil {
			return err
		}
		bin := filepath.Join(dir, "hrlens-agent")
		tmp := bin + ".new"
		// On Windows the program that updates itself is the background copy, without a console window
		if err := goBuild(p.goos, p.goarch, version, tmp, p.goos == "windows"); err != nil {
			return fmt.Errorf("building %s: %w", platform, err)
		}
		data, err := os.ReadFile(tmp)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		sha := hex.EncodeToString(sum[:])
		sig := ed25519.Sign(key, []byte(fmt.Sprintf("hrlens-agent %s %s %s", platform, version, sha)))
		info, _ := json.MarshalIndent(map[string]string{
			"version": version, "sha256": sha, "signature": base64.StdEncoding.EncodeToString(sig),
		}, "", "  ")

		// The program first, then info.json, so agents never see a version before its file is there
		if err := os.Rename(tmp, bin); err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(dir, "info.json"), info); err != nil {
			return err
		}
		fmt.Printf("%s %s  sha256 %s\n", platform, version, sha)

		switch p.goos {
		case "linux":
			err = linuxPackage(bin)
		case "windows":
			err = windowsPackage(bin, p.goarch, version)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// refuseOlder stops a release that installed agents would ignore (not newer than the last one).
func refuseOlder(dir, version string) error {
	data, err := os.ReadFile(filepath.Join(dir, "info.json"))
	if err != nil {
		return nil
	}
	var last struct{ Version string }
	json.Unmarshal(data, &last)
	if last.Version != "" && !greater(version, last.Version) {
		return fmt.Errorf("version %s is not higher than the published %s", version, last.Version)
	}
	return nil
}

// linuxPackage refreshes dist/hrlens-agent-linux (install.sh, README.txt) and its .tar.gz.
func linuxPackage(bin string) error {
	data, err := os.ReadFile(bin)
	if err != nil {
		return err
	}
	if err := os.WriteFile("dist/hrlens-agent-linux/hrlens-agent", data, 0o755); err != nil {
		return err
	}
	cmd := exec.Command("tar", "-C", "dist", "-czf", "dist/hrlens-agent-linux.tar.gz", "hrlens-agent-linux")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	fmt.Println("Install package: dist/hrlens-agent-linux.tar.gz")
	return nil
}

func goBuild(goos, goarch, version, out string, noConsole bool) error {
	ldflags := "-s -w -X main.version=" + version
	if noConsole {
		ldflags += " -H=windowsgui"
	}
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", out, "./cmd/agent")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// windowsPackage refreshes dist/hrlens-agent-windows (install.cmd, register.cmd, README.txt kept
// there) with both copies of the agent, and zips it into dist/hrlens-agent-windows.zip.
func windowsPackage(bgBin, goarch, version string) error {
	const dir = "dist/hrlens-agent-windows"
	data, err := os.ReadFile(bgBin)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "hrlens-agentw.exe"), data, 0o755); err != nil {
		return err
	}
	if err := goBuild("windows", goarch, version, filepath.Join(dir, "hrlens-agent.exe"), false); err != nil {
		return fmt.Errorf("building the Windows console copy: %w", err)
	}
	const zipPath = "dist/hrlens-agent-windows.zip"
	tmp := zipPath + ".new"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	for _, name := range []string{"hrlens-agent.exe", "hrlens-agentw.exe", "install.cmd", "register.cmd", "README.txt"} {
		if err = addToZip(zw, filepath.Join(dir, name), "hrlens-agent-windows/"+name); err != nil {
			break
		}
	}
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, zipPath); err != nil {
		return err
	}
	fmt.Println("Install package: " + zipPath)
	return nil
}

func addToZip(zw *zip.Writer, src, name string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, in)
	return err
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func greater(a, b string) bool {
	var x, y [3]int
	fmt.Sscanf(a, "%d.%d.%d", &x[0], &x[1], &x[2])
	fmt.Sscanf(b, "%d.%d.%d", &y[0], &y[1], &y[2])
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
