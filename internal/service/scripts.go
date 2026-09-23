package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
	"github.com/daviddwlee84/lazycrontab/internal/transport"
	"github.com/pelletier/go-toml/v2"
)

// ScriptTask records explicit user choices; execution needs only the compiled
// native cron command, not this sidecar or a lazycrontab installation.
type ScriptTask struct {
	Version int      `json:"version"`
	Preset  string   `json:"preset"`
	Runtime string   `json:"runtime,omitempty"`
	Project string   `json:"project,omitempty"`
	Args    []string `json:"args,omitempty"`
}

var ScriptPresets = []string{"command", "executable", "shell", "python", "uv-project", "uv-script"}
var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func ValidPreset(value string) bool {
	for _, preset := range ScriptPresets {
		if value == preset {
			return true
		}
	}
	return false
}

func ParseEnvironment(values []string) (map[string]string, error) {
	env := map[string]string{}
	for _, value := range values {
		key, content, ok := strings.Cut(value, "=")
		if !ok || !environmentName.MatchString(key) || strings.ContainsAny(content, "\x00\r\n") {
			return nil, fmt.Errorf("environment must be literal NAME=value, got %q", value)
		}
		env[key] = content
	}
	return env, nil
}

func scriptCommand(r Recipe) (string, error) {
	t := r.ScriptTask
	if t == nil {
		return "", fmt.Errorf("script task is required")
	}
	if t.Version != 1 {
		return "", fmt.Errorf("unsupported script task version %d", t.Version)
	}
	if !filepath.IsAbs(r.Script) || strings.ContainsAny(r.Script, "\r\n\x00") {
		return "", fmt.Errorf("choose an absolute target script path")
	}
	var argv []string
	switch t.Preset {
	case "executable":
		argv = []string{r.Script}
	case "shell", "python", "uv-project", "uv-script":
		if !filepath.IsAbs(t.Runtime) || strings.ContainsAny(t.Runtime, "\r\n\x00") {
			return "", fmt.Errorf("choose an absolute target runtime path")
		}
		switch t.Preset {
		case "shell", "python":
			argv = []string{t.Runtime, r.Script}
		case "uv-project":
			if !filepath.IsAbs(t.Project) || strings.ContainsAny(t.Project, "\r\n\x00") {
				return "", fmt.Errorf("choose a project directory")
			}
			argv = []string{t.Runtime, "run", "--project", t.Project, r.Script}
		case "uv-script":
			argv = []string{t.Runtime, "run", "--no-project", "--script", r.Script}
		}
	default:
		return "", fmt.Errorf("unknown script preset %q", t.Preset)
	}
	for _, arg := range t.Args {
		if strings.ContainsAny(arg, "\r\n\x00") {
			return "", fmt.Errorf("script arguments must each be a single line")
		}
	}
	return commandJoin(append(argv, t.Args...)), nil
}

// Separate percent characters at shell quoting boundaries. Cron's own
// backslash processing must not consume a literal backslash before a percent
// inside a generated argument (including filenames and nested Pueue payloads).
func commandQuote(value string) string {
	parts := strings.Split(value, "%")
	for i := range parts {
		parts[i] = transport.Quote(parts[i])
	}
	return strings.Join(parts, "'%'")
}
func commandJoin(argv []string) string {
	out := make([]string, len(argv))
	for i, arg := range argv {
		out[i] = commandQuote(arg)
	}
	return strings.Join(out, " ")
}
func commandPath(value string) string {
	if value == "~" {
		return "\"$HOME\""
	}
	if strings.HasPrefix(value, "~/") {
		return "\"$HOME\"/" + commandQuote(value[2:])
	}
	return commandQuote(value)
}

type PathCandidate struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}
type ScriptDiscovery struct {
	Exists         bool            "json:\"exists\""
	Readable       bool            "json:\"readable\""
	Base           string          `json:"base"`
	Script         string          `json:"script,omitempty"`
	Shebang        string          `json:"shebang,omitempty"`
	InlineMetadata bool            `json:"inline_metadata"`
	Projects       []PathCandidate `json:"projects"`
	Runtimes       []PathCandidate `json:"runtimes"`
}

func (d ScriptDiscovery) SuggestedPreset() string {
	if d.InlineMetadata {
		return "uv-script"
	}
	if strings.HasSuffix(d.Script, ".py") {
		if len(d.Projects) > 0 {
			return "uv-project"
		}
		return "python"
	}
	if d.Shebang != "" {
		return "executable"
	}
	return "shell"
}

func (d ScriptDiscovery) RuntimeFor(preset, name string) string {
	if strings.HasPrefix(preset, "uv-") {
		preset = "uv"
	}
	if name == "" && preset == "shell" {
		words := strings.Fields(d.Shebang)
		if len(words) > 0 {
			detected := filepath.Base(words[0])
			if detected == "env" {
				for _, word := range words[1:] {
					if !strings.HasPrefix(word, "-") {
						detected = word
						break
					}
				}
			}
			if detected == "sh" || detected == "bash" || detected == "zsh" {
				name = detected
			}
		}
	}
	for _, candidate := range d.Runtimes {
		if candidate.Kind == preset && (name == "" || filepath.Base(candidate.Path) == name) {
			return candidate.Path
		}
	}
	return ""
}

func (s *Service) targetBase(ctx context.Context, host string) (config.Host, string, string, error) {
	h, err := s.Config.Host(host)
	if err != nil {
		return h, "", "", err
	}
	result, err := s.Runner.Run(ctx, h, []string{"sh", "-c", `printf '%s' "$HOME"`}, nil)
	if err != nil {
		return h, "", "", err
	}
	home := string(result.Stdout)
	if !filepath.IsAbs(home) || strings.ContainsAny(home, "\r\n\x00") {
		return h, "", "", fmt.Errorf("target HOME is unavailable")
	}
	base := home
	if h.SSH == "" {
		base, err = os.Getwd()
		if err != nil {
			return h, "", "", err
		}
	}
	return h, base, home, nil
}

func resolveTargetPath(value, base, home string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("paths cannot contain newlines or NUL")
	}
	if value == "~" {
		value = home
	} else if strings.HasPrefix(value, "~/") {
		value = filepath.Join(home, value[2:])
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(base, value)
	}
	return filepath.Clean(value), nil
}

// ResolveTargetPath uses the same base shown by the script helper. A remote
// relative path never inherits the workstation's current directory.
func (s *Service) ResolveTargetPath(ctx context.Context, host, value, directory string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	_, base, home, err := s.targetBase(ctx, host)
	if err != nil {
		return "", err
	}
	if directory != "" {
		base, err = resolveTargetPath(directory, base, home)
		if err != nil {
			return "", err
		}
	}
	if value == "" {
		return base, nil
	}
	return resolveTargetPath(value, base, home)
}

// ResolveScriptRecipe freezes relative target paths and runtime choices before
// review. No script, shell profile, Python import or dependency manager runs.
func (s *Service) ResolveScriptRecipe(ctx context.Context, e Entry, r Recipe) (Recipe, error) {
	if r.ScriptTask == nil {
		return r, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	task := *r.ScriptTask
	task.Args = append([]string(nil), task.Args...)
	r.ScriptTask = &task
	_, base, home, err := s.targetBase(ctx, e.Host)
	if err != nil {
		return r, err
	}
	// Helper inputs use the selected account's HOME, exactly like discovery and
	// pickers. Crontab HOME still belongs to the eventual process environment.
	r.Directory, err = resolveTargetPath(r.Directory, base, home)
	if err != nil {
		return r, err
	}
	if r.Directory != "" {
		base = r.Directory
	}
	r.Script, err = resolveTargetPath(r.Script, base, home)
	if err != nil {
		return r, err
	}
	if r.Script == "" {
		return r, fmt.Errorf("script path is required")
	}
	task.Project, err = resolveTargetPath(task.Project, base, home)
	if err != nil {
		return r, err
	}
	if task.Preset == "uv-project" && task.Project == "" {
		return r, fmt.Errorf("choose a project directory, or pass --project")
	}
	if r.Directory == "" {
		r.Directory = filepath.Dir(r.Script)
		if task.Preset == "uv-project" {
			r.Directory = task.Project
		}
	}
	for _, value := range []*string{&r.Output, &r.Stderr, &r.Log} {
		*value, err = resolveTargetPath(*value, r.Directory, home)
		if err != nil {
			return r, err
		}
	}
	if task.Preset != "executable" {
		if task.Runtime != "" && strings.Contains(task.Runtime, "/") {
			task.Runtime, err = resolveTargetPath(task.Runtime, r.Directory, home)
			if err != nil {
				return r, err
			}
		} else {
			discovery, err := s.DiscoverScript(ctx, e.Host, r.Script)
			if err != nil {
				return r, err
			}
			selected := discovery.RuntimeFor(task.Preset, task.Runtime)
			if selected == "" {
				return r, fmt.Errorf("no %s runtime found on %s; choose a target executable with --runtime", task.Preset, e.Host)
			}
			task.Runtime = selected
		}
	}
	generated, err := scriptCommand(r)
	if err == nil {
		r.Original = generated
		if e.Dialect == schedule.System {
			r.Original = cronEscape(generated)
		}
	}
	return r, err
}

// Discovery only visits ancestors and bounded file headers. It never recursively
// scans a host or executes uv/Python to discover a project.
const discoverScript = `set -eu
p=$1
printf 'H\000%s\000' "$HOME"
if [ -f "$p" ]; then printf 'F\000yes\000'; fi
if [ -f "$p" ] && [ -r "$p" ]; then printf 'B\000'; head -c 32768 "$p" | tr -d '\000'; printf '\000'; fi
d=$p; [ -d "$d" ] || d=$(dirname "$d")
n=0
while [ "$n" -lt 64 ]; do
  if [ -f "$d/pyproject.toml" ]; then printf 'P\000%s\000' "$d"; fi
  if [ -x "$d/.venv/bin/python" ]; then printf 'python\000%s\000' "$d/.venv/bin/python"; fi
  [ "$d" != / ] || break
  d=$(dirname "$d"); n=$((n+1))
done
for name in sh bash zsh python3 python uv; do
  v=$(command -v "$name" 2>/dev/null || true)
  case "$v" in /*) [ -f "$v" ] && [ -x "$v" ] || continue;; *) continue;; esac
  case "$name" in python*) kind=python;; uv) kind=uv;; *) kind=shell;; esac
  printf '%s\000%s\000' "$kind" "$v"
done
for v in "$HOME/.local/bin/uv" /opt/homebrew/bin/uv /usr/local/bin/uv; do
  [ -f "$v" ] && [ -x "$v" ] && printf 'uv\000%s\000' "$v" || true
done`

func (s *Service) DiscoverScript(ctx context.Context, host, input string) (ScriptDiscovery, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	h, base, home, err := s.targetBase(ctx, host)
	d := ScriptDiscovery{Base: base, Projects: []PathCandidate{}, Runtimes: []PathCandidate{}}
	if err != nil {
		return d, err
	}
	p, err := resolveTargetPath(input, base, home)
	if err != nil {
		return d, err
	}
	if p == "" {
		p = base
	}
	d.Script = p
	result, err := s.Runner.Run(ctx, h, []string{"sh", "-c", discoverScript, "lazycrontab-discover", p}, nil)
	if err != nil {
		return d, err
	}
	parts := strings.Split(string(result.Stdout), "\x00")
	seen := map[string]bool{}
	for i := 0; i+1 < len(parts); i += 2 {
		kind, value := parts[i], parts[i+1]
		switch kind {
		case "F":
			d.Exists = value == "yes"
		case "B":
			d.Readable = true
			line, _, _ := strings.Cut(value, "\n")
			if strings.HasPrefix(line, "#!") {
				d.Shebang = strings.TrimSpace(strings.TrimPrefix(line, "#!"))
			}
			d.InlineMetadata = hasInlineMetadata(value)
		case "P":
			d.Projects = append(d.Projects, PathCandidate{value, "pyproject.toml"})
		case "shell", "python", "uv":
			if filepath.IsAbs(value) && !seen[value] {
				d.Runtimes = append(d.Runtimes, PathCandidate{value, kind})
				seen[value] = true
			}
		}
	}
	return d, nil
}

// PathCandidates lists only one directory (at most 200 entries), making it safe
// to call from an explicit picker on either a local or remote target.
func (s *Service) PathCandidates(ctx context.Context, host, input string, directories bool) ([]PathCandidate, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	h, base, home, err := s.targetBase(ctx, host)
	if err != nil {
		return nil, err
	}
	p, err := resolveTargetPath(input, base, home)
	if err != nil {
		return nil, err
	}
	if p == "" {
		p = base
	}
	script := `set -eu; d=$1; [ -d "$d" ] || d=$(dirname "$d"); printf 'directory\000%s\000' "$d"; n=0; for p in "$d"/* "$d"/.[!.]*; do [ -e "$p" ] || continue; if [ -d "$p" ]; then k=directory; else k=file; fi; [ "$2" != true ] || [ "$k" = directory ] || continue; printf '%s\000%s\000' "$k" "$p"; n=$((n+1)); [ "$n" -lt 200 ] || break; done`
	result, err := s.Runner.Run(ctx, h, []string{"sh", "-c", script, "lazycrontab-paths", p, fmt.Sprint(directories)}, nil)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(result.Stdout), "\x00")
	out := []PathCandidate{}
	for i := 0; i+1 < len(parts); i += 2 {
		if !strings.ContainsAny(parts[i+1], "\r\n") {
			out = append(out, PathCandidate{parts[i+1], parts[i]})
		}
	}
	if len(out) > 0 && filepath.Dir(out[0].Path) != out[0].Path {
		out = append(out, PathCandidate{filepath.Dir(out[0].Path), "directory"})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == "directory"
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func hasInlineMetadata(header string) bool {
	active := false
	var body strings.Builder
	for _, line := range strings.Split(strings.ReplaceAll(header, "\r\n", "\n"), "\n") {
		if !active {
			if line == "# /// script" {
				active = true
			}
			continue
		}
		if line == "# ///" {
			var data map[string]any
			if toml.Unmarshal([]byte(body.String()), &data) != nil {
				return false
			}
			deps, ok := data["dependencies"].([]any)
			if !ok {
				return false
			}
			for _, dep := range deps {
				if _, ok := dep.(string); !ok {
					return false
				}
			}
			return true
		}
		if !strings.HasPrefix(line, "#") {
			return false
		}
		body.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "#"), " "))
		body.WriteByte('\n')
	}
	return false
}
