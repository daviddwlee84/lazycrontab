package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
	"github.com/daviddwlee84/lazycrontab/internal/transport"
)

type Service struct {
	Config config.Config
	Runner transport.Runner
	Now    func() time.Time
}

func New(c config.Config) *Service {
	return &Service{Config: c, Runner: transport.Native{ConnectTimeout: c.ConnectTimeoutSeconds}, Now: time.Now}
}

type Entry struct {
	Runner string `json:"runner,omitempty"`
	document.Job
	Host        string           `json:"host"`
	Source      string           `json:"source"`
	Dialect     schedule.Dialect `json:"dialect"`
	Timezone    string           `json:"timezone,omitempty"`
	Description string           `json:"description"`
	Next        []time.Time      `json:"next"`
	Warnings    []string         `json:"warnings,omitempty"`
	ReadOnly    bool             `json:"read_only"`
}

func (e Entry) Key() string { return e.Host + "/" + e.Source + "/" + e.ID }

type Snapshot struct {
	Host     string             `json:"host"`
	Source   string             `json:"source"`
	Observed time.Time          `json:"observed"`
	Entries  []Entry            `json:"jobs"`
	Error    string             `json:"error,omitempty"`
	Timezone string             `json:"timezone,omitempty"`
	OS       string             `json:"os,omitempty"`
	Exists   bool               `json:"exists"`
	Document *document.Document `json:"-"`
}
type Plan struct {
	Host      string   `json:"host"`
	Source    string   `json:"source"`
	Operation string   `json:"operation"`
	Before    string   `json:"before"`
	After     string   `json:"after"`
	Revision  string   `json:"revision"`
	Existed   bool     `json:"existed"`
	JobID     string   `json:"job_id,omitempty"`
	Diff      string   `json:"diff"`
	Warnings  []string `json:"warnings,omitempty"`
}
type Receipt struct {
	Host     string `json:"host"`
	Source   string `json:"source"`
	Status   string `json:"status"`
	Backup   string `json:"backup,omitempty"`
	Revision string `json:"revision,omitempty"`
	Message  string `json:"message,omitempty"`
}
type Backup struct {
	FilePath string    `json:"file_path,omitempty"`
	ID       string    `json:"id"`
	Host     string    `json:"host"`
	Source   string    `json:"source"`
	Time     time.Time `json:"time"`
	Content  string    `json:"content"`
	Existed  bool      `json:"existed"`
}

func (s *Service) source(host, id string) (config.Host, config.Source, error) {
	if host == "all" || id == "all" {
		return config.Host{}, config.Source{}, fmt.Errorf("this operation requires one host and one source")
	}
	h, e := s.Config.Host(host)
	if e != nil {
		return h, config.Source{}, e
	}
	src, e := s.Config.Source(host, id)
	return h, src, e
}
func (s *Service) read(ctx context.Context, h config.Host, src config.Source) (string, bool, error) {
	if src.Kind == "user" {
		r, e := s.Runner.Run(ctx, h, []string{"env", "LC_ALL=C", "crontab", "-l"}, nil)
		if e != nil {
			if r.Code == 1 && len(r.Stdout) == 0 && strings.Contains(strings.ToLower(r.Stderr), "no crontab for") {
				return "", false, nil
			}
			return "", false, e
		}
		return string(r.Stdout), true, nil
	}
	p := transport.Path(src.Path)
	r, e := s.Runner.Run(ctx, h, []string{"sh", "-c", "if [ -f " + p + " ]; then cat " + p + "; elif [ -e " + p + " ]; then echo 'source is not a regular file' >&2; exit 1; else exit 44; fi"}, nil)
	if r.Code == 44 {
		return "", false, nil
	}
	return string(r.Stdout), e == nil, e
}
func (s *Service) facts(ctx context.Context, h config.Host, src config.Source) (string, *time.Location, string) {
	r, e := s.Runner.Run(ctx, h, []string{"sh", "-c", `uname -s; if [ -r /etc/timezone ]; then cat /etc/timezone; else readlink /etc/localtime 2>/dev/null || true; fi`}, nil)
	osname := ""
	zone := src.Timezone
	if zone == "" {
		zone = h.Timezone
	}
	if e == nil {
		lines := strings.Split(strings.TrimSpace(string(r.Stdout)), "\n")
		if len(lines) > 0 {
			osname = lines[0]
		}
		if zone == "" && len(lines) > 1 {
			zone = strings.TrimSpace(lines[1])
			if i := strings.Index(zone, "zoneinfo/"); i >= 0 {
				zone = zone[i+9:]
			}
		}
	}
	if zone != "" {
		if l, e := time.LoadLocation(zone); e == nil {
			return osname, l, zone
		}
	}
	if h.SSH == "" {
		return osname, time.Local, time.Local.String()
	}
	return osname, nil, ""
}
func (s *Service) Snapshot(ctx context.Context, host, id string) (Snapshot, error) {
	snap := Snapshot{Host: host, Source: id, Observed: s.Now(), Entries: []Entry{}}
	h, src, e := s.source(host, id)
	if e != nil {
		return snap, e
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.Config.ConnectTimeoutSeconds+10)*time.Second)
	defer cancel()
	raw, exists, e := s.read(ctx, h, src)
	if e != nil {
		return snap, e
	}
	snap.Exists = exists
	dialect := schedule.Dialect(src.Dialect)
	if dialect == "" {
		dialect = schedule.System
		if src.Kind == "file" {
			dialect = schedule.Supercronic
		}
	}
	snap.Document = document.Parse(raw, dialect, src.Kind == "system")
	osname, loc, zone := s.facts(ctx, h, src)
	snap.OS = osname
	snap.Timezone = zone
	for _, j := range snap.Document.Jobs {
		entry := Entry{Job: j, Host: host, Source: id, Dialect: dialect, Timezone: zone, Next: []time.Time{}, ReadOnly: src.ReadOnly || src.Kind == "system"}
		if recipe, err := LoadRecipe(entry); err == nil {
			entry.Runner = recipe.Runner
		} else {
			entry.Warnings = append(entry.Warnings, err.Error())
		}
		jl := loc
		if tz := j.Environment["CRON_TZ"]; tz != "" {
			if dialect == schedule.Supercronic || src.CronTZ {
				if l, err := time.LoadLocation(tz); err == nil {
					jl = l
					entry.Timezone = tz
				} else {
					jl = nil
					entry.Warnings = append(entry.Warnings, err.Error())
				}
			} else {
				entry.Warnings = append(entry.Warnings, "CRON_TZ runtime support is not confirmed; set source cron_tz=true only for a supporting daemon")
				if osname != "Darwin" {
					jl = nil
					entry.Timezone = ""
				}
			}
		}
		if j.Diagnostic == "" {
			sc, err := schedule.Parse(j.Schedule, dialect, jl, s.Config.Locale)
			if err != nil {
				entry.Diagnostic = err.Error()
			} else {
				entry.Description = sc.Description
				entry.Warnings = append(entry.Warnings, sc.Warnings...)
				if j.Enabled {
					entry.Next, _ = sc.NextN(ctx, s.Now(), 5)
				}
			}
		}
		snap.Entries = append(snap.Entries, entry)
	}
	return snap, nil
}
func (s *Service) Targets(host, source string) [][2]string {
	out := [][2]string{}
	hosts := s.Config.AllHosts()
	for _, h := range hosts {
		if host != "all" && h.ID != host {
			continue
		}
		for _, src := range s.Config.HostSources(h.ID) {
			if source != "all" && src.ID != source {
				continue
			}
			out = append(out, [2]string{h.ID, src.ID})
		}
	}
	return out
}
func (s *Service) List(ctx context.Context, host, source string) []Snapshot {
	targets := s.Targets(host, source)
	out := make([]Snapshot, len(targets))
	sem := make(chan struct{}, s.Config.MaxParallel)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t [2]string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				out[i] = Snapshot{Host: t[0], Source: t[1], Error: ctx.Err().Error()}
				return
			}
			snap, e := s.Snapshot(ctx, t[0], t[1])
			if e != nil {
				snap.Error = e.Error()
			}
			out[i] = snap
		}(i, t)
	}
	wg.Wait()
	return out
}
func Diff(before, after string) string {
	if before == after {
		return "No changes\n"
	}
	a := strings.Split(strings.TrimSuffix(before, "\n"), "\n")
	b := strings.Split(strings.TrimSuffix(after, "\n"), "\n")
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	x, y := len(a), len(b)
	for x > p && y > p && a[x-1] == b[y-1] {
		x--
		y--
	}
	var out strings.Builder
	out.WriteString("--- current\n+++ proposed\n")
	for _, l := range a[max(0, p-2):p] {
		out.WriteString(" " + l + "\n")
	}
	for _, l := range a[p:x] {
		out.WriteString("-" + l + "\n")
	}
	for _, l := range b[p:y] {
		out.WriteString("+" + l + "\n")
	}
	for _, l := range b[y:min(len(b), y+2)] {
		out.WriteString(" " + l + "\n")
	}
	return out.String()
}
func (s *Service) Plan(ctx context.Context, snap Snapshot, id string, replacement *document.Job, operation string) (Plan, error) {
	if snap.Document == nil {
		return Plan{}, fmt.Errorf("no source snapshot")
	}
	_, src, e := s.source(snap.Host, snap.Source)
	if e != nil {
		return Plan{}, e
	}
	if src.ReadOnly || src.Kind == "system" {
		return Plan{}, fmt.Errorf("source is read-only")
	}
	if replacement != nil {
		if _, e := schedule.Parse(replacement.Schedule, snap.Document.Dialect, time.UTC, s.Config.Locale); e != nil {
			return Plan{}, e
		}
		if replacement.Version != 1 || strings.HasPrefix(replacement.ID, "line-") || replacement.ID == "" {
			replacement.ID = document.NewID()
			replacement.Version = 1
		}
	}
	after, e := snap.Document.Change(id, replacement)
	if e != nil {
		return Plan{}, e
	}
	p := Plan{Host: snap.Host, Source: snap.Source, Operation: operation, Before: snap.Document.Raw, After: after, Revision: snap.Document.Revision, Existed: snap.Exists, Diff: Diff(snap.Document.Raw, after)}
	if replacement != nil {
		p.JobID = replacement.ID
	}
	if snap.Document.Dialect == schedule.Supercronic {
		p.Warnings = append(p.Warnings, "Saving the file does not confirm that a running Supercronic instance reloaded it.")
	}
	return p, nil
}
func (s *Service) backup(p Plan) (string, error) {
	base, e := config.Base("state")
	if e != nil {
		return "", e
	}
	b := Backup{ID: s.Now().UTC().Format("20060102T150405.000000000Z") + "-" + document.NewID()[:6], Host: p.Host, Source: p.Source, Time: s.Now(), Content: p.Before, Existed: p.Existed}
	if p.Operation == "script" {
		b.FilePath = p.Source
	}
	data, e := json.MarshalIndent(b, "", "  ")
	if e != nil {
		return "", e
	}
	path := filepath.Join(base, "backups", b.ID+".json")
	return path, config.AtomicWrite(path, data, 0600)
}
func (s *Service) Apply(ctx context.Context, p Plan) (Receipt, error) {
	r := Receipt{Host: p.Host, Source: p.Source, Status: "failed"}
	h, src, e := s.source(p.Host, p.Source)
	if e != nil {
		return r, e
	}
	if src.ReadOnly || src.Kind == "system" {
		return r, fmt.Errorf("source is read-only")
	}
	base, e := config.Base("state")
	if e != nil {
		return r, e
	}
	lock := filepath.Join(base, "locks", document.Digest(p.Host+"/"+p.Source)+".lock")
	if e = os.MkdirAll(filepath.Dir(lock), 0700); e != nil {
		return r, e
	}
	f, e := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return r, fmt.Errorf("source locked by another operation (%s): %w", lock, e)
	}
	f.Close()
	defer os.Remove(lock)
	raw, exists, e := s.read(ctx, h, src)
	if e != nil {
		return r, e
	}
	if document.Digest(raw) != p.Revision || raw != p.Before || exists != p.Existed {
		return r, fmt.Errorf("source changed since review; refresh and review again")
	}
	if p.Before == p.After {
		r.Status = "unchanged"
		return r, nil
	}
	if src.Kind == "file" && (src.Dialect == "" || src.Dialect == "supercronic") {
		check, e := s.Runner.Run(ctx, h, []string{"sh", "-c", "if command -v supercronic >/dev/null 2>&1; then exec supercronic -test /dev/stdin; else exit 44; fi"}, []byte(p.After))
		if e != nil && check.Code != 44 {
			return r, fmt.Errorf("Supercronic validation: %w", e)
		}
	}
	r.Backup, e = s.backup(p)
	if e != nil {
		return r, fmt.Errorf("backup failed; source unchanged: %w", e)
	}
	if src.Kind == "user" {
		_, e = s.Runner.Run(ctx, h, []string{"env", "LC_ALL=C", "crontab", "-"}, []byte(p.After))
	} else {
		e = s.writeFile(ctx, h, src.Path, p.Before, p.After, p.Existed)
	}
	if e != nil {
		r.Status = "unknown"
		r.Message = "Write may have reached the target. Refresh before retrying."
		return r, e
	}
	current, _, e := s.read(ctx, h, src)
	if e != nil || current != p.After {
		r.Status = "unknown"
		return r, fmt.Errorf("write sent but read-back did not confirm the proposed content")
	}
	r.Status = "saved"
	r.Revision = document.Digest(current)
	return r, nil
}
func (s *Service) writeFile(ctx context.Context, h config.Host, path, before, after string, exists bool) error {
	p := transport.Path(path)
	test := "[ ! -e \"$p\" ]"
	if exists {
		test = "[ -f \"$p\" ] && printf '%s' " + transport.Quote(before) + " | cmp -s \"$p\" -"
	}
	script := `set -eu; p=` + p + `; [ ! -L "$p" ] || { echo 'refusing to replace a symlink; configure its resolved path' >&2; exit 1; }; ` + test + ` || { echo 'file changed since review' >&2; exit 1; }; t=$(mktemp "${p}.lazycrontab.XXXXXX"); trap 'rm -f "$t"' EXIT HUP INT TERM; if [ -f "$p" ]; then cp -p "$p" "$t"; else chmod 600 "$t"; fi; cat > "$t"; ` + test + ` || { echo 'file changed during write' >&2; exit 1; }; mv -f "$t" "$p"`
	_, e := s.Runner.Run(ctx, h, []string{"sh", "-c", script}, []byte(after))
	return e
}
func Backups() ([]Backup, error) {
	base, e := config.Base("state")
	if e != nil {
		return nil, e
	}
	files, e := filepath.Glob(filepath.Join(base, "backups", "*.json"))
	if e != nil {
		return nil, e
	}
	out := []Backup{}
	for i := len(files) - 1; i >= 0; i-- {
		b, e := os.ReadFile(files[i])
		if e != nil {
			return nil, e
		}
		var item Backup
		if e = json.Unmarshal(b, &item); e != nil {
			return nil, e
		}
		out = append(out, item)
	}
	return out, nil
}
func (s *Service) RestorePlan(ctx context.Context, id string) (Plan, error) {
	bs, e := Backups()
	if e != nil {
		return Plan{}, e
	}
	for _, b := range bs {
		if b.ID == id {
			snap, e := s.Snapshot(ctx, b.Host, b.Source)
			if e != nil {
				return Plan{}, e
			}
			return Plan{Host: b.Host, Source: b.Source, Operation: "restore", Before: snap.Document.Raw, After: b.Content, Revision: snap.Document.Revision, Existed: snap.Exists, Diff: Diff(snap.Document.Raw, b.Content)}, nil
		}
	}
	return Plan{}, fmt.Errorf("backup not found")
}
func (s *Service) Reload(ctx context.Context, host, id string) (Receipt, error) {
	h, src, e := s.source(host, id)
	r := Receipt{Host: host, Source: id, Status: "reload-failed"}
	if e != nil {
		return r, e
	}
	if len(src.Reload) == 0 {
		return r, fmt.Errorf("no reload argv configured for this source")
	}
	_, e = s.Runner.Run(ctx, h, src.Reload, nil)
	if e == nil {
		r.Status = "reload-command-succeeded"
		r.Message = "Command completed; runtime adoption is not independently verified."
	}
	return r, e
}
func ReadJSON(path string, out any) error {
	b, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	return json.Unmarshal(b, out)
}
