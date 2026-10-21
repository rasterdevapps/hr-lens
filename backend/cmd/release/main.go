// Command release builds and signs a desktop agent version. Installed agents pick it up
// from the server within about an hour.
//
//	go run ./cmd/release -genkey            once: creates the release key (keep agent-release.key safe)
//	go run ./cmd/release -version 0.2.0     builds and signs version 0.2.0 into a NEW staging folder
//
// It stages, for each platform, release-staging/agent/<os>-<arch>/hrlens-agent and info.json (served to
// agents by the server), and the install packages release-staging/hrlens-agent-linux.tar.gz and
// release-staging/hrlens-agent-windows.zip. On Windows the self-updating program is the background copy
// (hrlens-agentw.exe, no console window); the package also has the console copy for sign-in.
// Run it from the backend folder.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
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
	"strings"
)

const (
	keyFile       = "agent-release.key"      // private key: never share it or put it on employee computers
	pubKeyFile    = "cmd/agent/updatekey.go" // public key, built into the agent
	defaultOutput = "release-staging"
)

// Platforms the agent can capture on so far
var platforms = []struct{ goos, goarch string }{{"linux", "amd64"}, {"windows", "amd64"}}

var versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func main() {
	genkey := flag.Bool("genkey", false, "create the release key pair (once)")
	version := flag.String("version", "", "version to build, e.g. 0.2.0 (higher than the last one)")
	out := flag.String("out", defaultOutput, "new staging directory; never use a live served directory")
	dev := flag.Bool("dev", false, "build unsigned development packages without reading a release key")
	signer := flag.String("windows-signer", "", "executable that signs and verifies one Windows binary passed as its only argument")
	flag.Parse()

	var err error
	switch {
	case *genkey:
		err = generateKey()
	case *dev || versionPattern.MatchString(*version):
		err = buildPackages(*version, *out, *dev, *signer)
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
	if src, err := os.ReadFile(pubKeyFile); err == nil && !bytes.Contains(src, []byte(`const updateKey = ""`)) {
		return errors.New("an agent public key already exists; recover the matching private key instead of replacing trust")
	}
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

// buildPackages creates a new, isolated staging directory. Deployment is a separate step.
// The signer must apply Authenticode and verify it, returning nonzero on any failure.
func buildPackages(version, out string, dev bool, signer string) error {
	var key ed25519.PrivateKey
	var err error
	if dev {
		version = "dev" // Development artifacts must never replace installed signed agents.
	} else {
		if !versionPattern.MatchString(version) {
			return errors.New("a semantic release version is required")
		}
		if signer == "" {
			return errors.New("production Windows packages require -windows-signer; use -dev for unsigned testing")
		}
		key, err = loadKey()
		if err != nil {
			return err
		}
		source, err := os.ReadFile(pubKeyFile)
		if err != nil {
			return err
		}
		expected := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
		if !bytes.Contains(source, []byte(`const updateKey = "`+expected+`"`)) {
			return errors.New("release private key does not match the existing agent public key")
		}
	}
	if err := os.Mkdir(out, 0o755); err != nil {
		return fmt.Errorf("staging directory must be new: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			fmt.Fprintln(os.Stderr, "Incomplete staging directory retained for diagnostics:", out)
		}
	}()
	for _, p := range platforms {
		platform := p.goos + "-" + p.goarch
		dir := filepath.Join(out, "agent", platform)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		bin := filepath.Join(dir, "hrlens-agent")
		if p.goos == "windows" {
			bin += ".exe"
		}
		if err := goBuild(p.goos, p.goarch, version, bin, p.goos == "windows"); err != nil {
			return err
		}
		if p.goos == "windows" && !dev {
			if err := signWindows(signer, bin); err != nil {
				return err
			}
		}
		data, err := os.ReadFile(bin)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		sha := hex.EncodeToString(sum[:])
		// Existing update endpoint serves an extensionless filename, even for Windows.
		updateBin := filepath.Join(dir, "hrlens-agent")
		if bin != updateBin {
			if err := os.Rename(bin, updateBin); err != nil {
				return err
			}
			bin = updateBin
		}
		if !dev {
			sig := ed25519.Sign(key, []byte(fmt.Sprintf("hrlens-agent %s %s %s", platform, version, sha)))
			info, _ := json.MarshalIndent(map[string]string{"version": version, "sha256": sha, "signature": base64.StdEncoding.EncodeToString(sig)}, "", "  ")
			if err := writeAtomic(filepath.Join(dir, "info.json"), info); err != nil {
				return err
			}
		}
		pkg := filepath.Join(out, "hrlens-agent-"+p.goos)
		if err := copyTemplates(filepath.Join("packaging", p.goos), pkg); err != nil {
			return err
		}
		if p.goos == "windows" {
			if err := os.WriteFile(filepath.Join(pkg, "hrlens-agentw.exe"), data, 0o755); err != nil {
				return err
			}
			cli := filepath.Join(pkg, "hrlens-agent.exe")
			if err := goBuild("windows", p.goarch, version, cli, false); err != nil {
				return err
			}
			if !dev {
				if err := signWindows(signer, cli); err != nil {
					return err
				}
			}
		} else {
			if err := os.WriteFile(filepath.Join(pkg, "hrlens-agent"), data, 0o755); err != nil {
				return err
			}
		}
		if dev {
			if err := os.WriteFile(filepath.Join(pkg, "DEVELOPMENT.txt"), []byte("Unsigned development build. No automatic updates. Not a production release.\n"), 0o644); err != nil {
				return err
			}
		}
		if err := packageChecksums(pkg); err != nil {
			return err
		}
		if err := archivePackage(pkg, p.goos == "windows"); err != nil {
			return err
		}
		fmt.Printf("Staged %s %s (sha256 %s)\n", platform, version, sha)
	}
	complete = true
	fmt.Println("Packages staged in", out, "; nothing was published or deployed.")
	return nil
}

func signWindows(signer, path string) error {
	cmd := exec.Command(signer, path)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Windows signing/verification failed: %w", err)
	}
	return nil
}

func copyTemplates(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			return fmt.Errorf("unexpected installer template %s", e.Name())
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(e.Name(), ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, mode); err != nil {
			return err
		}
	}
	return nil
}

func packageChecksums(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	checksums := map[string]string{}
	for _, e := range entries {
		if !e.Type().IsRegular() || e.Name() == "SHA256SUMS.json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		checksums[e.Name()] = hex.EncodeToString(sum[:])
	}
	data, err := json.MarshalIndent(checksums, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS.json"), data, 0o644)
}

// archivePackage uses Go's archives on both platforms; no external tar/zip dependency.
func archivePackage(dir string, windows bool) (err error) {
	suffix := ".tar.gz"
	if windows {
		suffix = ".zip"
	}
	f, err := os.Create(dir + suffix)
	if err != nil {
		return err
	}
	defer func() {
		if e := f.Close(); err == nil {
			err = e
		}
	}()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if windows {
		zw := zip.NewWriter(f)
		for _, e := range entries {
			if err = addToZip(zw, filepath.Join(dir, e.Name()), filepath.Base(dir)+"/"+e.Name()); err != nil {
				break
			}
		}
		if e := zw.Close(); err == nil {
			err = e
		}
		return err
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		info, eerr := e.Info()
		if eerr != nil {
			err = eerr
			break
		}
		h, eerr := tar.FileInfoHeader(info, "")
		if eerr != nil {
			err = eerr
			break
		}
		h.Name = filepath.Base(dir) + "/" + e.Name()
		// Windows file modes do not carry POSIX executable bits. Archive the
		// Linux package with target permissions, independent of build host.
		h.Mode = 0o644
		if strings.HasSuffix(e.Name(), ".sh") || e.Name() == "hrlens-agent" {
			h.Mode = 0o755
		}
		if err = tw.WriteHeader(h); err != nil {
			break
		}
		data, eerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if eerr != nil {
			err = eerr
			break
		}
		if _, err = tw.Write(data); err != nil {
			break
		}
	}
	if e := tw.Close(); err == nil {
		err = e
	}
	if e := gz.Close(); err == nil {
		err = e
	}
	return err
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
