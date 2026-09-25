// Package upgrade updates the resolved installed executable, never a PATH shadow.
package upgrade

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/daviddwlee84/lazycrontab/internal/brewupgrade"
	"github.com/daviddwlee84/lazycrontab/internal/managedupgrade"
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
	brewPlan   *brewupgrade.Plan
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
	managed, managedErr := brewupgrade.Prepare(ctx, exe, "lazycrontab", brewupgrade.Options{Inspect: managedupgrade.Inspect(managedupgrade.Product{Binary: "lazycrontab", Module: Module, Main: Module})})
	if managedErr == nil {
		p.Owner = "homebrew"
		p.Strategy = "homebrew"
		p.Supported = true
		p.Brew = managed.BrewPath
		p.Formula = managed.Formula
		p.Current = managed.CurrentVersion
		p.Reason = "The verified owning Homebrew chooses its available version."
		p.brewPlan = &managed
		return p, nil
	}
	if !errors.Is(managedErr, brewupgrade.ErrNotManaged) {
		return p, managedErr
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
		p.Reason = "Development, modified or unrecognized build is preserved. For a chezmoi-managed release use just upgrade-personal; otherwise use the original installer or rebuild the checkout."
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
		if p.brewPlan == nil || p.Brew != p.brewPlan.BrewPath || p.Formula != p.brewPlan.Formula || p.Executable != p.brewPlan.CurrentPath {
			return "", fmt.Errorf("Homebrew plan changed or was not verified")
		}
		result, err := p.brewPlan.Apply(ctx, progress)
		if err != nil {
			return "", err
		}
		return result.Path + "\n" + result.Version, nil
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
