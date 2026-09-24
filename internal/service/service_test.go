package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
	"github.com/daviddwlee84/lazycrontab/internal/transport"
)

type fakeCron struct {
	mu                  sync.Mutex
	raw                 string
	exists              bool
	failRead, failWrite bool
	writes              int
}

func (f *fakeCron) Run(_ context.Context, h config.Host, args []string, in []byte) (transport.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "crontab -l"):
		if f.failRead {
			return transport.Result{Code: 1, Stderr: "permission denied"}, errors.New("permission denied")
		}
		if !f.exists {
			return transport.Result{Code: 1, Stderr: "no crontab for fixture"}, errors.New("no crontab")
		}
		return transport.Result{Stdout: []byte(f.raw)}, nil
	case strings.HasSuffix(joined, "crontab -"):
		f.writes++
		f.raw = string(in)
		f.exists = true
		if f.failWrite {
			return transport.Result{Code: 255}, errors.New("connection lost")
		}
		return transport.Result{}, nil
	case strings.Contains(joined, "uname -s"):
		return transport.Result{Stdout: []byte("Linux\nUTC\n")}, nil
	default:
		return transport.Result{}, fmt.Errorf("unexpected fixture command: %s", joined)
	}
}
func fixtureService(t *testing.T, raw string) (*Service, *fakeCron) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	f := &fakeCron{raw: raw, exists: raw != ""}
	s := New(config.Defaults())
	s.Runner = f
	s.Now = func() time.Time { return time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC) }
	return s, f
}
func TestPlanIsReadOnlyAndApplyDetectsConflict(t *testing.T) {
	s, f := fixtureService(t, "# keep\nPATH=/bin\n0 0 * * * echo old\n")
	snap, e := s.Snapshot(context.Background(), "local", "user")
	if e != nil {
		t.Fatal(e)
	}
	job := snap.Document.Jobs[0]
	job.Command = "echo new"
	p, e := s.Plan(context.Background(), snap, job.ID, &job, "edit")
	if e != nil {
		t.Fatal(e)
	}
	if f.writes != 0 {
		t.Fatal("planning wrote")
	}
	bs, _ := Backups()
	if len(bs) != 0 {
		t.Fatal("planning made a backup")
	}
	f.raw += "# external edit\n"
	if _, e = s.Apply(context.Background(), p); e == nil {
		t.Fatal("lost update")
	}
	if f.writes != 0 {
		t.Fatal("conflict wrote")
	}
	f.raw = p.Before
	r, e := s.Apply(context.Background(), p)
	if e != nil || r.Status != "saved" {
		t.Fatalf("%+v %v", r, e)
	}
	if !strings.HasPrefix(f.raw, "# keep\nPATH=/bin\n") {
		t.Fatal(f.raw)
	}
	bs, e = Backups()
	if e != nil || len(bs) != 1 || bs[0].Content != p.Before {
		t.Fatal(bs, e)
	}
}
func TestUnknownOutcomeIsNotRetried(t *testing.T) {
	s, f := fixtureService(t, "")
	snap, e := s.Snapshot(context.Background(), "local", "user")
	if e != nil {
		t.Fatal(e)
	}
	j := document.Job{Schedule: "* * * * *", Command: "echo hello", Enabled: true}
	p, _ := s.Plan(context.Background(), snap, "", &j, "add")
	f.failWrite = true
	r, e := s.Apply(context.Background(), p)
	if e == nil || r.Status != "unknown" || f.writes != 1 {
		t.Fatal(r, e, f.writes)
	}
	if !strings.Contains(f.raw, "echo hello") {
		t.Fatal("fixture write not dispatched")
	}
}
func TestReadFailureIsNotEmpty(t *testing.T) {
	s, f := fixtureService(t, "")
	f.failRead = true
	if _, e := s.Snapshot(context.Background(), "local", "user"); e == nil {
		t.Fatal("permission failure treated as an empty crontab")
	}
}
func TestNativeFileTransactionAndScriptPermissions(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "script ' with spaces.sh")
	if e := os.WriteFile(path, []byte("#!/bin/sh\necho before\n"), 0750); e != nil {
		t.Fatal(e)
	}
	s := New(config.Defaults())
	d, e := s.ScriptDraft(context.Background(), "local", path)
	if e != nil {
		t.Fatal(e)
	}
	defer os.Remove(d.File)
	r, e := s.SaveScript(context.Background(), d, "#!/bin/sh\necho after\n")
	if e != nil || r.Status != "saved" {
		t.Fatal(r, e)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0750 {
		t.Fatal(st.Mode())
	}
	if _, e = s.SaveScript(context.Background(), d, "stale"); e == nil {
		t.Fatal("stale script overwrote newer file")
	}
	link := filepath.Join(dir, "link")
	os.Symlink(path, link)
	if e = s.writeFile(context.Background(), config.Host{ID: "local"}, link, "#!/bin/sh\necho after\n", "bad", true); e == nil {
		t.Fatal("symlink replaced")
	}
}
func TestWrapperPreservesPercentQuotingAndOutput(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := t.TempDir()
	out := filepath.Join(dir, "out ' $literal")
	j := Entry{Job: document.Job{Metadata: document.Metadata{ID: "x", Version: 1}, Command: `printf '\%s\n' 'a # quote' '$(touch SHOULD_NOT_EXIST)'`, Environment: map[string]string{}}, Dialect: schedule.System}
	r := Recipe{Runner: "direct", Output: out}
	compiled, e := Compile(j, r)
	if e != nil {
		t.Fatal(e)
	}
	code, stdin := SplitPercent(compiled)
	native := transport.Native{}
	result, e := native.Run(context.Background(), config.Host{ID: "local"}, []string{"sh", "-c", code}, []byte(stdin))
	if e != nil {
		t.Fatal(result, e)
	}
	b, _ := os.ReadFile(out)
	if string(b) != "a # quote\n$(touch SHOULD_NOT_EXIST)\n" {
		t.Fatalf("payload changed: %q\n%s", b, compiled)
	}
	j.Command = "cat%hello%world"
	compiled, e = Compile(j, Recipe{Runner: "direct", Output: filepath.Join(dir, "stdin")})
	if e != nil {
		t.Fatal(e)
	}
	code, stdin = SplitPercent(compiled)
	if _, e = native.Run(context.Background(), config.Host{ID: "local"}, []string{"sh", "-c", code}, []byte(stdin)); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "stdin"))
	if string(b) != "hello\nworld\n" {
		t.Fatalf("stdin semantics: %q", b)
	}
}
func TestPueueWrapperOnlyEnqueuesExactPayload(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "pueue")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + transport.Quote(capture) + "\nprintf '42\\n'\n"
	os.WriteFile(fake, []byte(script), 0700)
	j := Entry{Job: document.Job{Metadata: document.Metadata{ID: "job"}, Command: `printf '\%s' 'quoted $HOME'`}, Dialect: schedule.System}
	compiled, e := Compile(j, Recipe{Runner: "pueue", PueuePath: fake, Group: "group ' with spaces"})
	if e != nil {
		t.Fatal(e)
	}
	code, input := SplitPercent(compiled)
	_, e = (transport.Native{}).Run(context.Background(), config.Host{ID: "local"}, []string{"sh", "-c", code}, []byte(input))
	if e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(capture)
	if !strings.Contains(string(b), "group ' with spaces\n") || !strings.Contains(string(b), "quoted $HOME") {
		t.Fatalf("argv changed: %s", b)
	}
}
func TestWrapperRejectsAmbiguousRelativeDirectory(t *testing.T) {
	if _, e := Compile(Entry{Job: document.Job{Command: "echo ok"}}, Recipe{Directory: "relative"}); e == nil {
		t.Fatal("relative directory could resolve twice in a Pueue wrapper")
	}
}
func TestAgendaGlobalOrderAndDST(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	loc, _ := time.LoadLocation("America/New_York")
	date := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	entries := []Entry{}
	for i, expr := range []string{"45 1 * * *", "15 1 * * *"} {
		entries = append(entries, Entry{Job: document.Job{Metadata: document.Metadata{ID: fmt.Sprint(i)}, Schedule: expr, Enabled: true}, Timezone: loc.String(), Dialect: schedule.System})
	}
	snaps := []Snapshot{{Entries: entries}}
	rows, more, e := Agenda(context.Background(), snaps, date, date.AddDate(0, 0, 1), 0, 10)
	if e != nil || more || len(rows) != 4 {
		t.Fatal(rows, more, e)
	}
	for i := 1; i < len(rows); i++ {
		if !rows[i].Time.After(rows[i-1].Time) {
			t.Fatal(rows)
		}
	}
	o, e := BuildOverview(context.Background(), snaps, date, loc, 20)
	if e != nil {
		t.Fatal(e)
	}
	if o.Cells[6][1].Count != 4 {
		t.Fatal(o.Cells[6][1])
	}
}
