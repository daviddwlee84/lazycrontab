package document

import (
	"strings"
	"testing"

	"github.com/daviddwlee84/lazycrontab/internal/schedule"
)

func TestEditPreservesUnrelatedBytes(t *testing.T) {
	prefix := "# heading\r\n\nSHELL=/bin/bash\nEMPTY=\"\"\n# literal example\nweird extension\n"
	line := "  0  3 * * * printf 'has # hash and \\%s' '專案 👩🏽‍💻'  >> /tmp/x\n"
	suffix := "# trailing\nPATH=/opt/bin:/bin"
	raw := prefix + line + suffix
	d := Parse(raw, schedule.System, false)
	if d.Raw != raw || len(d.Jobs) != 1 {
		t.Fatalf("round-trip/jobs: %#v", d)
	}
	j := d.Jobs[0]
	if !strings.Contains(j.Command, "# hash") || j.Environment["SHELL"] != "/bin/bash" {
		t.Fatal(j)
	}
	j.Name = "backup"
	updated, e := d.Change(j.ID, &j)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(updated, prefix) || !strings.HasSuffix(updated, suffix) {
		t.Fatal(updated)
	}
	again := Parse(updated, schedule.System, false)
	if len(again.Jobs) != 1 || again.Jobs[0].Name != "backup" {
		t.Fatal(again.Jobs)
	}
	removed, e := again.Change(again.Jobs[0].ID, nil)
	if e != nil || removed != prefix+suffix {
		t.Fatalf("remove changed neighbors: %q %v", removed, e)
	}
}
func TestDisabledMetadataAndDuplicates(t *testing.T) {
	j := Job{Metadata: Metadata{Version: 1, ID: "abc", Name: "job"}, Schedule: "0 0 * * *", Command: "echo yes", Enabled: false}
	raw, e := Parse("# plain commented job\n# * * * * * echo example\n", schedule.System, false).Change("", &j)
	if e != nil {
		t.Fatal(e)
	}
	d := Parse(raw, schedule.System, false)
	if len(d.Jobs) != 1 || d.Jobs[0].Enabled {
		t.Fatal(d.Jobs)
	}
	d = Parse(raw+raw, schedule.System, false)
	if _, e = d.Change("abc", &j); e == nil {
		t.Fatal("duplicate ID was writable")
	}
}
func TestSupercronicEnvironmentIsDocumentGlobal(t *testing.T) {
	d := Parse("X=first\n* * * * * echo a\nX=last\n0 0 1 1 * 2028 echo b\n", schedule.Supercronic, false)
	if len(d.Jobs) != 2 {
		t.Fatal(d.Jobs)
	}
	for _, j := range d.Jobs {
		if j.Environment["X"] != "last" {
			t.Fatal(j)
		}
	}
	if d.Jobs[1].Schedule != "0 0 1 1 * 2028" {
		t.Fatal(d.Jobs[1])
	}
}
func TestSystemFileUserAndReadOnly(t *testing.T) {
	d := Parse("0 0 * * * root /bin/true\n", schedule.System, true)
	if d.Jobs[0].User != "root" || d.Jobs[0].Command != "/bin/true" {
		t.Fatal(d.Jobs)
	}
	if _, e := d.Change(d.Jobs[0].ID, nil); e == nil {
		t.Fatal("system file writable")
	}
}
func TestEditingRemarkRetainsEscapedTrailingCommandSpace(t *testing.T) {
	raw := "0 0 * * * printf x\\ \n"
	d := Parse(raw, schedule.System, false)
	j := d.Jobs[0]
	if j.Command != "printf x\\ " {
		t.Fatalf("lost shell-significant trailing space: %q", j.Command)
	}
	j.Remark = "note"
	after, e := d.Change(j.ID, &j)
	if e != nil || !strings.HasSuffix(after, "0 0 * * * printf x\\ \n") {
		t.Fatal(after, e)
	}
}
func FuzzRoundTrip(f *testing.F) {
	f.Add("# comment\nPATH=/bin\n* * * * * echo hello\n")
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 65536 {
			return
		}
		d := Parse(s, schedule.System, false)
		if d.Raw != s || strings.Join(d.lines, "") != s {
			t.Fatal("changed bytes")
		}
	})
}
