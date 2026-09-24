package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/transport"
)

// MaxManagedScriptBytes bounds editor/CLI input and target-side reads equally.
const MaxManagedScriptBytes = 1 << 20

// ManagedScript describes a native script file, not an execution dependency on
// lazycrontab. The actual target file remains authoritative.
type ManagedScript struct {
	Version int    `json:"version"`
	Digest  string `json:"digest"`
}

// ManagedScriptPlan is immutable once reviewed. Publishing a new version before
// switching the cron command leaves existing schedules and Pueue tasks intact.
type ManagedScriptPlan struct {
	Host           string `json:"host"`
	Path           string `json:"path"`
	Content        string `json:"content"`
	Digest         string `json:"digest"`
	PreviousPath   string `json:"previous_path,omitempty"`
	PreviousDigest string `json:"previous_digest,omitempty"`
}

var managedDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var errManagedIntegrity = errors.New("managed script integrity check failed")

func validateManagedContent(content string) error {
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("managed script content is required")
	}
	if len(content) > MaxManagedScriptBytes {
		return fmt.Errorf("managed script exceeds the %d-byte limit", MaxManagedScriptBytes)
	}
	if strings.ContainsRune(content, '\x00') {
		return fmt.Errorf("managed script cannot contain NUL bytes")
	}
	return nil
}

// IsManagedScriptPath identifies the reserved immutable version namespace. It
// also protects versions when their helper metadata is absent or out of date.
func IsManagedScriptPath(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\r\n\x00") {
		return false
	}
	dir := filepath.Dir(path)
	scripts := filepath.Dir(dir)
	return managedDigest.MatchString(strings.TrimSuffix(filepath.Base(path), ".sh")) &&
		strings.HasSuffix(path, ".sh") && managedDigest.MatchString(filepath.Base(dir)) &&
		filepath.Base(scripts) == "scripts" && filepath.Base(filepath.Dir(scripts)) == "lazycrontab"
}

func (s *Service) refuseManagedScriptMutation(ctx context.Context, h config.Host, path string) error {
	immutable := func() error {
		return fmt.Errorf("managed script versions are immutable; edit the managed job to create and review a new version")
	}
	if IsManagedScriptPath(filepath.Clean(path)) {
		return immutable()
	}
	// Explicit --path can reach a managed file through a parent symlink or ~/.
	// Resolve only the parent on the target; writeFile still refuses a symlink
	// at the file itself and retains the original reviewed path for external files.
	code := `set -eu; p=` + transport.Path(path) + `; d=$(dirname "$p"); b=$(basename "$p"); cd "$d"; printf '%s/%s' "$(pwd -P)" "$b"`
	result, err := s.Runner.Run(ctx, h, []string{"sh", "-c", code}, nil)
	if err != nil {
		return fmt.Errorf("resolve script path before saving: %w", err)
	}
	if IsManagedScriptPath(filepath.Clean(string(result.Stdout))) {
		return immutable()
	}
	return nil
}

func validateManagedPath(e Entry, path, digest string) error {
	if e.ID == "" || e.Host == "" || e.Source == "" || !managedDigest.MatchString(digest) || !IsManagedScriptPath(path) ||
		filepath.Base(path) != digest+".sh" || filepath.Base(filepath.Dir(path)) != document.Digest(e.Key()) {
		return fmt.Errorf("%w: invalid managed script path or job identity", errManagedIntegrity)
	}
	return nil
}

func validateManagedRecipe(e Entry, r Recipe) error {
	if r.ManagedScript == nil || r.ManagedScript.Version != 1 || r.ScriptTask == nil || r.ScriptTask.Preset != "shell" {
		return fmt.Errorf("%w: managed scripts require version 1 and the shell preset", errManagedIntegrity)
	}
	return validateManagedPath(e, r.Script, r.ManagedScript.Digest)
}

func validateManagedPlan(e Entry, p ManagedScriptPlan) error {
	if p.Host != e.Host {
		return fmt.Errorf("managed script plan belongs to another host")
	}
	if err := validateManagedContent(p.Content); err != nil {
		return err
	}
	if document.Digest(p.Content) != p.Digest {
		return fmt.Errorf("%w: reviewed script content changed", errManagedIntegrity)
	}
	if p.PreviousPath != "" || p.PreviousDigest != "" {
		if err := validateManagedPath(e, p.PreviousPath, p.PreviousDigest); err != nil {
			return fmt.Errorf("previous script revision: %w", err)
		}
	}
	return validateManagedPath(e, p.Path, p.Digest)
}

func (s *Service) checkManagedPrevious(ctx context.Context, h config.Host, p ManagedScriptPlan) error {
	if p.PreviousPath == "" {
		return nil
	}
	content, exists, err := s.readManagedFile(ctx, h, p.PreviousPath)
	if err != nil {
		return fmt.Errorf("check previous managed script revision: %w", err)
	}
	if !exists || document.Digest(content) != p.PreviousDigest {
		return fmt.Errorf("%w: previous script changed since review; reload the job and review again", errManagedIntegrity)
	}
	return nil
}

func (s *Service) managedDataBase(ctx context.Context, h config.Host) (string, error) {
	if h.SSH == "" {
		return config.Base("data")
	}
	result, err := s.Runner.Run(ctx, h, []string{"sh", "-c", `printf '%s\000%s\000' "${XDG_DATA_HOME-}" "$HOME"`}, nil)
	if err != nil {
		return "", fmt.Errorf("read target XDG data directory: %w", err)
	}
	parts := strings.Split(string(result.Stdout), "\x00")
	if len(parts) != 3 || parts[2] != "" {
		return "", fmt.Errorf("target XDG data directory response is invalid")
	}
	base := parts[0]
	if !filepath.IsAbs(base) {
		if !filepath.IsAbs(parts[1]) || strings.ContainsAny(parts[1], "\r\n") {
			return "", fmt.Errorf("target HOME is unavailable")
		}
		base = filepath.Join(parts[1], ".local", "share")
	}
	if strings.ContainsAny(base, "\r\n") {
		return "", fmt.Errorf("target XDG data directory cannot contain newlines")
	}
	return filepath.Join(base, "lazycrontab"), nil
}

// PrepareManagedScript only discovers the target's data directory and inspects
// any existing version. It never creates target files or directories.
func (s *Service) PrepareManagedScript(ctx context.Context, e Entry, content string) (ManagedScriptPlan, error) {
	if err := validateManagedContent(content); err != nil {
		return ManagedScriptPlan{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	h, err := s.Config.Host(e.Host)
	if err != nil {
		return ManagedScriptPlan{}, err
	}
	base, err := s.managedDataBase(ctx, h)
	if err != nil {
		return ManagedScriptPlan{}, err
	}
	digest := document.Digest(content)
	p := ManagedScriptPlan{Host: e.Host, Path: filepath.Join(base, "scripts", document.Digest(e.Key()), digest+".sh"), Content: content, Digest: digest}
	if err = validateManagedPlan(e, p); err != nil {
		return ManagedScriptPlan{}, err
	}
	if _, _, err = s.inspectManagedPlan(ctx, h, p); err != nil {
		return ManagedScriptPlan{}, err
	}
	return p, nil
}

// Only the directories owned by this helper are checked. XDG_DATA_HOME itself
// can be an existing user-selected directory or symlink; its mode is not changed.
const inspectManagedScript = `set -eu
root=$1; scripts=$2; job=$3; p=$4; limit=$5
for d in "$root" "$scripts" "$job"; do
  if [ -L "$d" ] || { [ -e "$d" ] && [ ! -d "$d" ]; }; then
    echo 'managed script directory is not an ordinary directory' >&2; exit 45
  fi
done
if [ -L "$p" ]; then echo 'managed script cannot be a symlink' >&2; exit 45; fi
if [ ! -e "$p" ]; then exit 44; fi
if [ ! -f "$p" ] || [ ! -r "$p" ]; then echo 'managed script is not a readable regular file' >&2; exit 45; fi
head -c "$limit" "$p"`

func managedScriptArgs(script, path string) []string {
	job := filepath.Dir(path)
	scripts := filepath.Dir(job)
	root := filepath.Dir(scripts)
	return []string{"sh", "-c", script, "lazycrontab-managed-script", root, scripts, job, path, fmt.Sprint(MaxManagedScriptBytes + 1)}
}

func (s *Service) readManagedFile(ctx context.Context, h config.Host, path string) (string, bool, error) {
	result, err := s.Runner.Run(ctx, h, managedScriptArgs(inspectManagedScript, path), nil)
	if result.Code == 44 {
		return "", false, nil
	}
	if result.Code == 45 {
		return "", false, fmt.Errorf("%w: %s", errManagedIntegrity, strings.TrimSpace(result.Stderr))
	}
	if err != nil {
		return "", false, err
	}
	content := string(result.Stdout)
	if len(content) > MaxManagedScriptBytes {
		return "", true, fmt.Errorf("%w: target script exceeds the size limit", errManagedIntegrity)
	}
	return content, true, nil
}

func (s *Service) inspectManagedPlan(ctx context.Context, h config.Host, p ManagedScriptPlan) (string, bool, error) {
	content, exists, err := s.readManagedFile(ctx, h, p.Path)
	if err != nil {
		return "", exists, err
	}
	if exists && (content != p.Content || document.Digest(content) != p.Digest) {
		return "", true, fmt.Errorf("%w: the target version was changed; it will not be overwritten", errManagedIntegrity)
	}
	return content, exists, nil
}

// ReadManagedScript refuses a substituted or modified version. It reads the
// stored absolute path even when the user's XDG preference has since changed.
func (s *Service) ReadManagedScript(ctx context.Context, e Entry, r Recipe) (string, error) {
	if err := validateManagedRecipe(e, r); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	h, err := s.Config.Host(e.Host)
	if err != nil {
		return "", err
	}
	content, exists, err := s.readManagedFile(ctx, h, r.Script)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("%w: target script does not exist: %s", errManagedIntegrity, r.Script)
	}
	if document.Digest(content) != r.ManagedScript.Digest {
		return "", fmt.Errorf("%w: target script content changed; restore its reviewed version or explicitly create a new script", errManagedIntegrity)
	}
	return content, nil
}

// A hard-link publication never replaces an existing version. Temporary files
// have no cron references and are cleaned on failure; published versions stay.
const installManagedScript = `set -eu
umask 077
root=$1; scripts=$2; job=$3; p=$4
mkdir -p "$(dirname "$root")"
for d in "$root" "$scripts" "$job"; do
  if [ -L "$d" ] || { [ -e "$d" ] && [ ! -d "$d" ]; }; then
    echo 'managed script directory is not an ordinary directory' >&2; exit 45
  fi
  if [ ! -d "$d" ]; then mkdir "$d"; fi
done
if [ -L "$p" ] || [ -e "$p" ]; then echo 'managed script version already exists; refresh before retrying' >&2; exit 45; fi
t=$(mktemp "$job/.lazycrontab.XXXXXX")
trap 'rm -f "$t"' EXIT HUP INT TERM
cat > "$t"
ln "$t" "$p"`

func (s *Service) installManagedScript(ctx context.Context, h config.Host, p ManagedScriptPlan) (string, error) {
	if _, exists, err := s.inspectManagedPlan(ctx, h, p); err != nil {
		return "failed", err
	} else if exists {
		return "unchanged", nil
	}
	if _, err := s.Runner.Run(ctx, h, managedScriptArgs(installManagedScript, p.Path), []byte(p.Content)); err != nil {
		return "unknown", fmt.Errorf("managed script write was not confirmed: %w", err)
	}
	if _, exists, err := s.inspectManagedPlan(ctx, h, p); err != nil || !exists {
		if err != nil {
			return "unknown", fmt.Errorf("managed script read-back: %w", err)
		}
		return "unknown", fmt.Errorf("managed script read-back did not find the published version")
	}
	return "saved", nil
}
