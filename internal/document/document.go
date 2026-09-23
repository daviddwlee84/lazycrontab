// Package document retains unedited bytes, including unfamiliar cron syntax.
package document

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aptible/supercronic/cronexpr"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
)

const Marker = "# lazycrontab: "
const Disabled = "# lazycrontab-disabled: "

type Metadata struct {
	Version int    `json:"v"`
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Remark  string `json:"remark,omitempty"`
}
type Job struct {
	Metadata
	Schedule    string            `json:"schedule"`
	Command     string            `json:"command"`
	Enabled     bool              `json:"enabled"`
	Line        int               `json:"line"`
	User        string            `json:"user,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Diagnostic  string            `json:"diagnostic,omitempty"`
	start, end  int
}
type Document struct {
	Raw         string
	Revision    string
	Jobs        []Job
	Dialect     schedule.Dialect
	SystemFile  bool
	Environment map[string]string
	lines       []string
}

var tokens = regexp.MustCompile(`\S+`)
var assignment = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$`)

func Digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func NewID() string          { var b [12]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func cloneEnv(env map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range env {
		out[k] = v
	}
	return out
}

func Parse(raw string, dialect schedule.Dialect, systemFile bool) *Document {
	d := &Document{Raw: raw, Revision: Digest(raw), Dialect: dialect, SystemFile: systemFile, Jobs: []Job{}}
	d.lines = strings.SplitAfter(raw, "\n")
	env := map[string]string{}
	seen := map[string]int{}
	for i, line := range d.lines {
		body := strings.TrimSuffix(line, "\n")
		if strings.HasSuffix(line, "\r\n") {
			body = strings.TrimSuffix(body, "\r")
		}
		trim := strings.TrimLeft(body, " \t")
		if m := assignment.FindStringSubmatch(strings.TrimSpace(trim)); m != nil {
			v := m[2]
			if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') && v[len(v)-1] == v[0] {
				v = v[1 : len(v)-1]
			}
			env[m[1]] = v
			continue
		}
		enabled := true
		if strings.HasPrefix(trim, Disabled) {
			enabled = false
			trim = strings.TrimPrefix(trim, Disabled)
		} else if strings.HasPrefix(trim, "#") || trim == "" {
			continue
		}
		j, ok := parseJob(trim, dialect, systemFile)
		if !ok {
			continue
		}
		j.Enabled = enabled
		j.Line = i + 1
		j.start = i
		j.end = i + 1
		j.Environment = cloneEnv(env)
		if i > 0 && strings.HasPrefix(strings.TrimSpace(d.lines[i-1]), Marker) {
			var m Metadata
			if json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(d.lines[i-1]), Marker)), &m) == nil && m.Version == 1 && m.ID != "" {
				j.Metadata = m
				j.start = i - 1
			}
		}
		if j.ID == "" {
			j.ID = fmt.Sprintf("line-%d-%s", i+1, Digest(line)[:12])
		} else {
			seen[j.ID]++
		}
		if _, err := schedule.Parse(j.Schedule, dialect, time.UTC, "en"); err != nil {
			j.Diagnostic = err.Error()
		}
		d.Jobs = append(d.Jobs, j)
	}
	for i := range d.Jobs {
		if seen[d.Jobs[i].ID] > 1 {
			d.Jobs[i].Diagnostic = "duplicate lazycrontab ID; repair metadata before editing"
		}
		if dialect == schedule.Supercronic {
			d.Jobs[i].Environment = cloneEnv(env)
		}
	}
	d.Environment = cloneEnv(env)
	return d
}

func parseJob(line string, dialect schedule.Dialect, systemFile bool) (Job, bool) {
	idx := tokens.FindAllStringIndex(line, -1)
	if len(idx) < 2 {
		return Job{}, false
	}
	counts := []int{5}
	if strings.HasPrefix(line, "@") {
		counts = []int{1}
	} else if dialect == schedule.Supercronic {
		counts = []int{7, 6, 5}
	}
	for _, n := range counts {
		extra := 0
		if systemFile {
			extra = 1
		}
		if len(idx) <= n+extra {
			continue
		}
		expr := line[:idx[n-1][1]]
		if dialect == schedule.Supercronic {
			if _, err := cronexpr.ParseStrict(expr); err != nil {
				continue
			}
		}
		j := Job{Schedule: expr, Command: line[idx[n+extra][0]:]}
		if systemFile {
			j.User = line[idx[n][0]:idx[n][1]]
		}
		return j, true
	}
	return Job{}, false
}

func (d *Document) Find(id string) (Job, error) {
	for _, j := range d.Jobs {
		if j.ID == id {
			return j, nil
		}
	}
	return Job{}, fmt.Errorf("job %q not found; use list to obtain its ID", id)
}
func render(j Job) (string, error) {
	if strings.ContainsAny(j.Command, "\r\n\x00") || strings.TrimSpace(j.Command) == "" {
		return "", fmt.Errorf("command must be one nonempty cron line; use a script for multiline commands")
	}
	if strings.ContainsAny(j.Schedule, "\r\n\x00") {
		return "", fmt.Errorf("schedule must be one line")
	}
	if j.Version != 1 || j.ID == "" || strings.HasPrefix(j.ID, "line-") {
		j.ID = NewID()
	}
	j.Version = 1
	meta, _ := json.Marshal(j.Metadata)
	line := strings.Join(strings.Fields(j.Schedule), " ") + " " + j.Command
	if !j.Enabled {
		line = Disabled + line
	}
	return Marker + string(meta) + "\n" + line + "\n", nil
}

// Change replaces a single entry, leaving every other byte untouched.
// Empty id appends a new entry; nil replacement removes the selected entry.
func (d *Document) Change(id string, replacement *Job) (string, error) {
	if d.SystemFile {
		return "", fmt.Errorf("system crontab sources are read-only")
	}
	start, end := len(d.lines), len(d.lines)
	if id != "" {
		j, err := d.Find(id)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(j.Diagnostic, "duplicate") {
			return "", fmt.Errorf("%s", j.Diagnostic)
		}
		start, end = j.start, j.end
	}
	var content string
	if replacement != nil {
		var err error
		content, err = render(*replacement)
		if err != nil {
			return "", err
		}
	}
	prefix := strings.Join(d.lines[:start], "")
	if id == "" && prefix != "" && !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	return prefix + content + strings.Join(d.lines[end:], ""), nil
}
