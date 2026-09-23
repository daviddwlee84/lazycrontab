// Package upgrade updates the resolved installed executable, never a PATH shadow.
package upgrade

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const Module = "github.com/daviddwlee84/lazycrontab"

type Plan struct {
	Current    string `json:"current"`
	Candidate  string `json:"candidate,omitempty"`
	Executable string `json:"executable"`
	Digest     string `json:"digest"`
	Strategy   string `json:"strategy"`
	Owner      string `json:"owner"`
	Supported  bool   `json:"supported"`
	Reason     string `json:"reason,omitempty"`
	Brew       string `json:"brew,omitempty"`
	Formula    string `json:"formula,omitempty"`
}

func digestFile(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func Check(ctx context.Context, current string) (Plan, error) {
	exe, e := os.Executable()
	if e != nil {
		return Plan{}, e
	}
	return CheckPath(ctx, current, exe, "https://api.github.com/repos/daviddwlee84/lazycrontab/releases/latest")
}
func CheckPath(ctx context.Context, current, exe, endpoint string) (Plan, error) {
	resolved, e := filepath.EvalSymlinks(exe)
	if e != nil {
		return Plan{}, e
	}
	p := Plan{Current: current, Executable: resolved, Strategy: "unsupported", Owner: "unknown"}
	p.Digest, e = digestFile(resolved)
	if e != nil {
		return p, e
	}
	if i := strings.Index(resolved, string(filepath.Separator)+"Cellar"+string(filepath.Separator)); i >= 0 {
		prefix := resolved[:i]
		rest := strings.Split(resolved[i+8:], string(filepath.Separator))
		if len(rest) < 3 {
			return p, nil
		}
		formula := rest[0]
		receipt := filepath.Join(prefix, "Cellar", rest[0], rest[1], "INSTALL_RECEIPT.json")
		var data map[string]any
		b, e := os.ReadFile(receipt)
		if e != nil {
			return p, fmt.Errorf("cannot verify Homebrew receipt: %w", e)
		}
		if e = json.Unmarshal(b, &data); e != nil {
			return p, e
		}
		brew := filepath.Join(prefix, "bin", "brew")
		if _, e = os.Stat(brew); e != nil {
			return p, e
		}
		out, e := exec.CommandContext(ctx, brew, "--cellar").Output()
		cellar, resolveErr := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
		if e != nil || resolveErr != nil || cellar != filepath.Join(prefix, "Cellar") {
			return p, fmt.Errorf("Homebrew Cellar does not match executable owner")
		}
		p.Owner = "homebrew"
		p.Strategy = "homebrew"
		p.Supported = true
		p.Brew = brew
		p.Formula = formula
		p.Reason = "The owning Homebrew chooses its available version."
		return p, nil
	}
	if strings.Contains(resolved, "/nix/store/") || strings.Contains(resolved, "/mise/") {
		p.Owner = "package-manager"
		p.Reason = "Use the owning package manager; direct replacement is disabled."
		return p, nil
	}
	info, e := buildinfo.ReadFile(resolved)
	if e != nil {
		return p, e
	}
	dev := info.Main.Version == "" || info.Main.Version == "(devel)" || info.Main.Replace != nil || info.Main.Path != Module
	for _, setting := range info.Settings {
		if setting.Key == "vcs.modified" && setting.Value == "true" {
			dev = true
		}
	}
	if dev {
		p.Owner = "development"
		p.Reason = "Development, modified or unrecognized build is preserved. Install a published source tag to enable source upgrades."
		return p, nil
	}
	p.Owner = "source"
	p.Strategy = "source"
	req, e := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if e != nil {
		return p, e
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 15 * time.Second}
	response, e := client.Do(req)
	if e != nil {
		return p, e
	}
	defer response.Body.Close()
	if response.StatusCode == 404 {
		p.Reason = "No published stable release is available."
		return p, nil
	}
	if response.StatusCode != 200 {
		return p, fmt.Errorf("release lookup returned %s", response.Status)
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
	}
	if e = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&release); e != nil {
		return p, e
	}
	if release.Draft || release.Prerelease || !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(release.Tag) {
		return p, fmt.Errorf("release is not a stable version tag")
	}
	p.Candidate = release.Tag
	p.Supported = true
	return p, nil
}
func Apply(ctx context.Context, p Plan, progress io.Writer) (string, error) {
	if !p.Supported {
		return "", fmt.Errorf("upgrade unavailable: %s", p.Reason)
	}
	digest, e := digestFile(p.Executable)
	if e != nil || digest != p.Digest {
		return "", fmt.Errorf("installed executable changed after review")
	}
	if p.Strategy == "homebrew" {
		cmd := exec.CommandContext(ctx, p.Brew, "upgrade", p.Formula)
		cmd.Stdout = progress
		cmd.Stderr = progress
		if e = cmd.Run(); e != nil {
			return "", e
		}
		prefix, e := exec.CommandContext(ctx, p.Brew, "--prefix", p.Formula).Output()
		if e != nil {
			return "", e
		}
		path := filepath.Join(strings.TrimSpace(string(prefix)), "bin", "lazycrontab")
		out, e := exec.CommandContext(ctx, path, "--version").Output()
		return path + "\n" + strings.TrimSpace(string(out)), e
	}
	if p.Strategy != "source" {
		return "", fmt.Errorf("unsupported upgrade strategy")
	}
	if p.Current == p.Candidate {
		return "Already at " + p.Current, nil
	}
	goPath, e := exec.LookPath("go")
	if e != nil {
		return "", fmt.Errorf("source upgrade requires an installed compatible Go toolchain")
	}
	lock := p.Executable + ".upgrade-lock"
	f, e := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", fmt.Errorf("another updater may be active: %w", e)
	}
	f.Close()
	defer os.Remove(lock)
	dir, e := os.MkdirTemp(filepath.Dir(p.Executable), ".lazycrontab-upgrade-*")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(dir)
	cmd := exec.CommandContext(ctx, goPath, "install", Module+"@"+p.Candidate)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOBIN="+dir, "GOWORK=off", "GOTOOLCHAIN=local")
	cmd.Stdout = progress
	cmd.Stderr = progress
	if e = cmd.Run(); e != nil {
		return "", e
	}
	candidate := filepath.Join(dir, "lazycrontab")
	info, e := buildinfo.ReadFile(candidate)
	if e != nil {
		return "", e
	}
	if info.Main.Path != Module || info.Main.Version != p.Candidate || info.Main.Replace != nil {
		return "", fmt.Errorf("staged executable identity does not match release")
	}
	verifyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, e := exec.CommandContext(verifyCtx, candidate, "--version").Output()
	if e != nil || !strings.Contains(string(out), p.Candidate) {
		return "", fmt.Errorf("staged version check failed")
	}
	if e = ctx.Err(); e != nil {
		return "", e
	}
	digest, e = digestFile(p.Executable)
	if e != nil || digest != p.Digest {
		return "", fmt.Errorf("executable changed during build")
	}
	infoOld, e := os.Stat(p.Executable)
	if e != nil {
		return "", e
	}
	if !infoOld.Mode().IsRegular() {
		return "", fmt.Errorf("destination is no longer a regular file")
	}
	if e = os.Chmod(candidate, infoOld.Mode().Perm()); e != nil {
		return "", e
	}
	if e = os.Rename(candidate, p.Executable); e != nil {
		return "", e
	}
	return "Updated " + p.Executable + " to " + p.Candidate, nil
}
