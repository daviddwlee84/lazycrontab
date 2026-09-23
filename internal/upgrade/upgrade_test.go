package upgrade

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevelopmentBuildPreserved(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("development check should not need a release lookup")
	}))
	defer server.Close()
	p, e := CheckPath(context.Background(), "v9.0.0", exe, server.URL)
	if e != nil {
		t.Fatal(e)
	}
	if p.Supported || p.Owner != "development" {
		t.Fatal(p)
	}
	if _, e = Apply(context.Background(), p, io.Discard); e == nil {
		t.Fatal("development binary overwritten")
	}
}
func TestHomebrewOwnerAndNoopVersion(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FIXTURE_BREW", root)
	keg := filepath.Join(root, "Cellar", "lazycrontab", "1.0.0")
	os.MkdirAll(filepath.Join(keg, "bin"), 0700)
	binary := filepath.Join(keg, "bin", "lazycrontab")
	os.WriteFile(binary, []byte("#!/bin/sh\necho 'lazycrontab version v1.0.0'\n"), 0700)
	os.WriteFile(filepath.Join(keg, "INSTALL_RECEIPT.json"), []byte("{}"), 0600)
	os.MkdirAll(filepath.Join(root, "bin"), 0700)
	os.MkdirAll(filepath.Join(root, "opt"), 0700)
	os.Symlink(keg, filepath.Join(root, "opt", "lazycrontab"))
	os.WriteFile(filepath.Join(root, "bin", "brew"), []byte(`#!/bin/sh
case "$1" in
 --cellar) echo "$FIXTURE_BREW/Cellar";;
 --prefix) echo "$FIXTURE_BREW/opt/lazycrontab";;
 upgrade) printf '%s\n' "$@" > "$FIXTURE_BREW/invoked";;
 *) exit 2;;
esac
`), 0700)
	p, e := CheckPath(context.Background(), "v1.0.0", binary, "invalid-release-endpoint")
	if e != nil || !p.Supported || p.Owner != "homebrew" {
		t.Fatal(p, e)
	}
	if _, e = os.Stat(filepath.Join(root, "invoked")); !os.IsNotExist(e) {
		t.Fatal("check invoked upgrade")
	}
	message, e := Apply(context.Background(), p, io.Discard)
	if e != nil || !strings.Contains(message, "v1.0.0") {
		t.Fatal(message, e)
	}
	b, _ := os.ReadFile(filepath.Join(root, "invoked"))
	if string(b) != "upgrade\nlazycrontab\n" {
		t.Fatal(string(b))
	}
	os.WriteFile(binary, []byte("changed"), 0700)
	if _, e = Apply(context.Background(), p, io.Discard); e == nil {
		t.Fatal("changed executable accepted")
	}
}

// A file-backed Go proxy provides actual versioned module build metadata. This
// verifies the source upgrade path without publishing or downloading our repo.
func TestSourceUpgradeUpdatesMovedCopyNotGOBIN(t *testing.T) {
	if testing.Short() {
		t.Skip("actual Go source build")
	}
	goPath, e := exec.LookPath("go")
	if e != nil {
		t.Skip("Go unavailable")
	}
	root := t.TempDir()
	proxy := filepath.Join(root, "proxy")
	versions := filepath.Join(proxy, filepath.FromSlash(Module), "@v")
	os.MkdirAll(versions, 0700)
	for _, version := range []string{"v1.0.0", "v1.0.1"} {
		mod := []byte("module " + Module + "\n\ngo 1.26.6\n")
		main := []byte("package main\nimport \"fmt\"\nfunc main(){fmt.Println(\"lazycrontab version " + version + "\")}\n")
		os.WriteFile(filepath.Join(versions, version+".mod"), mod, 0600)
		info, _ := json.Marshal(map[string]string{"Version": version, "Time": "2026-09-23T00:00:00Z"})
		os.WriteFile(filepath.Join(versions, version+".info"), info, 0600)
		f, e := os.Create(filepath.Join(versions, version+".zip"))
		if e != nil {
			t.Fatal(e)
		}
		z := zip.NewWriter(f)
		for name, content := range map[string][]byte{"go.mod": mod, "main.go": main} {
			w, e := z.Create(Module + "@" + version + "/" + name)
			if e != nil {
				t.Fatal(e)
			}
			w.Write(content)
		}
		z.Close()
		f.Close()
	}
	os.WriteFile(filepath.Join(versions, "list"), []byte("v1.0.0\nv1.0.1\n"), 0600)
	t.Setenv("GOPROXY", "file://"+filepath.ToSlash(proxy))
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GOMODCACHE", filepath.Join(root, "modcache"))
	t.Cleanup(func() {
		filepath.WalkDir(filepath.Join(root, "modcache"), func(path string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(path, 0700)
			}
			return nil
		})
	})
	t.Setenv("GOWORK", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	initial := filepath.Join(root, "initial")
	os.Mkdir(initial, 0700)
	t.Setenv("GOBIN", initial)
	cmd := exec.Command(goPath, "install", Module+"@v1.0.0")
	cmd.Dir = root
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fixture install: %s %v", out, e)
	}
	custom := filepath.Join(root, "custom")
	os.Mkdir(custom, 0700)
	moved := filepath.Join(custom, "lazycrontab")
	if e = os.Rename(filepath.Join(initial, "lazycrontab"), moved); e != nil {
		t.Fatal(e)
	}
	shadow := filepath.Join(root, "shadow")
	os.Mkdir(shadow, 0700)
	os.WriteFile(filepath.Join(shadow, "lazycrontab"), []byte("keep this PATH shadow"), 0600)
	t.Setenv("GOBIN", shadow)
	t.Setenv("PATH", shadow+string(os.PathListSeparator)+os.Getenv("PATH"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"tag_name":"v1.0.1","prerelease":false,"draft":false}`)
	}))
	defer server.Close()
	p, e := CheckPath(context.Background(), "v1.0.0", moved, server.URL)
	if e != nil || !p.Supported || p.Candidate != "v1.0.1" {
		t.Fatal(p, e)
	}
	if _, e = Apply(context.Background(), p, io.Discard); e != nil {
		t.Fatal(e)
	}
	out, e := exec.Command(moved, "--version").Output()
	if e != nil || !strings.Contains(string(out), "v1.0.1") {
		t.Fatal(string(out), e)
	}
	b, _ := os.ReadFile(filepath.Join(shadow, "lazycrontab"))
	if string(b) != "keep this PATH shadow" {
		t.Fatal("overwrote unrelated GOBIN copy")
	}
}
