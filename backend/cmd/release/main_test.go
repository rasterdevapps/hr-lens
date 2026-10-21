package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPackageFromMaintainedTemplates(t *testing.T) {
	for _, platform := range []string{"linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "hrlens-agent-"+platform)
			if err := copyTemplates(filepath.Join("..", "..", "packaging", platform), dir); err != nil {
				t.Fatal(err)
			}
			name := "hrlens-agent"
			if platform == "windows" {
				name += "w.exe"
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte("synthetic executable"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := packageChecksums(dir); err != nil {
				t.Fatal(err)
			}
			if err := archivePackage(dir, platform == "windows"); err != nil {
				t.Fatal(err)
			}
			files := map[string][]byte{}
			if platform == "windows" {
				z, err := zip.OpenReader(dir + ".zip")
				if err != nil {
					t.Fatal(err)
				}
				defer z.Close()
				for _, f := range z.File {
					r, err := f.Open()
					if err != nil {
						t.Fatal(err)
					}
					data, err := io.ReadAll(r)
					r.Close()
					if err != nil {
						t.Fatal(err)
					}
					files[filepath.Base(f.Name)] = data
				}
			} else {
				f, err := os.Open(dir + ".tar.gz")
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				gz, err := gzip.NewReader(f)
				if err != nil {
					t.Fatal(err)
				}
				defer gz.Close()
				tr := tar.NewReader(gz)
				for {
					h, err := tr.Next()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					data, err := io.ReadAll(tr)
					if err != nil {
						t.Fatal(err)
					}
					files[filepath.Base(h.Name)] = data
					if filepath.Ext(h.Name) == ".sh" && h.Mode&0o111 == 0 {
						t.Fatal("Linux installer lost executable permission")
					}
				}
			}
			if len(files["README.txt"]) == 0 || len(files[name]) == 0 {
				t.Fatal("missing package content")
			}
			var sums map[string]string
			if err := json.Unmarshal(files["SHA256SUMS.json"], &sums); err != nil {
				t.Fatal(err)
			}
			if len(sums) != len(files)-1 {
				t.Fatal("checksum manifest does not cover the package")
			}
			for name, want := range sums {
				sum := sha256.Sum256(files[name])
				if hex.EncodeToString(sum[:]) != want {
					t.Errorf("checksum mismatch for %s", name)
				}
			}
		})
	}
}

func TestProductionRequiresWindowsSigning(t *testing.T) {
	out := filepath.Join(t.TempDir(), "release")
	if err := buildPackages("1.2.3", out, false, ""); err == nil {
		t.Fatal("unsigned production build accepted")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("failed validation created staging directory")
	}
}

func TestStagingNeverOverwritesExistingDirectory(t *testing.T) {
	out := t.TempDir()
	if err := buildPackages("", out, true, ""); err == nil {
		t.Fatal("existing output directory accepted")
	}
}
