package hostinventory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSSHDiscoveryIsStaticAndPreservesUncertainty(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".ssh", "config")
	fixtureFile(t, root, "Include \"fragments with spaces/*.conf\"\nHost=lab other # not-an-alias\n HostName example.invalid\nMatch exec \"touch /should-never-run\"\n Include conditional.conf\nHost * !excluded\n IdentityFile ~/.ssh/not-read\nHost exact-after-match\n")
	fixtureFile(t, filepath.Join(home, ".ssh", "fragments with spaces", "a.conf"), "Host first\n HostName example.invalid\n")
	fixtureFile(t, filepath.Join(home, ".ssh", "fragments with spaces", "b.conf"), "Host second\n")
	fixtureFile(t, filepath.Join(home, ".ssh", "conditional.conf"), "Host guarded\n")
	inv, err := ReadSSH(context.Background(), root, home)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Complete || len(inv.Diagnostics) == 0 {
		t.Fatal("conditional inventory reported complete", inv)
	}
	want := map[string]bool{"first": true, "second": true, "lab": true, "other": true, "guarded": false, "exact-after-match": true}
	if len(inv.Candidates) != len(want) {
		t.Fatal(inv.Candidates)
	}
	for _, c := range inv.Candidates {
		selectable, ok := want[c.Alias]
		if !ok || c.Selectable != selectable || len(c.Sources) == 0 {
			t.Fatal("incorrect candidate", c)
		}
	}
}

func TestSSHCyclesMalformedAndDynamicIncludes(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".ssh", "config")
	fixtureFile(t, root, "Include loop.conf\nHost *\nInclude ${CONFIG}/remote.conf\nHost \"broken\nHost uncertain\n")
	fixtureFile(t, filepath.Join(home, ".ssh", "loop.conf"), "Include config\nHost loop-host\n")
	inv, err := ReadSSH(context.Background(), root, home)
	if err != nil || inv.Complete || len(inv.Diagnostics) < 3 {
		t.Fatal(inv, err)
	}
	for _, c := range inv.Candidates {
		if c.Alias == "uncertain" && c.Selectable {
			t.Fatal("malformed context became usable", c)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadSSH(ctx, root, home); err == nil {
		t.Fatal("cancelled scan proceeded")
	}
}

func TestDevSchemaUsesOnlySelectableActiveAliases(t *testing.T) {
	raw := []byte(`{"schema_version":1,"kind":"ssh_list","complete":false,"aliases":[{"name":"lab","status":"active","selectable":true,"definitions":[{"alias":"do-not-scrape","source":{"path":"fixture","line":7}}],"fleet":[{"name":"research","ssh_alias":"also-not-a-candidate"}]},{"name":"blocked","status":"conflict","selectable":false},{"name":"unknown","status":"unknown","selectable":true},{"name":"inactive","status":"inactive","selectable":false}],"diagnostics":[{"message":"incomplete fixture"}]}`)
	inv, err := DecodeDev(raw)
	if err != nil || inv.Complete || len(inv.Candidates) != 4 || len(inv.Diagnostics) != 1 {
		t.Fatal(inv, err)
	}
	for _, c := range inv.Candidates {
		if c.Selectable != (c.Alias == "lab") {
			t.Fatal(c)
		}
	}
	for _, raw := range []string{`{"schema_version":2,"kind":"ssh_list"}`, `{"schema_version":1,"kind":"ssh_machines"}`, `{"schema_version":1,"kind":"ssh_list","aliases":[{"name":"x"},{"name":"x"}]}`} {
		if _, err := DecodeDev([]byte(raw)); err == nil {
			t.Fatal("accepted unsupported document", raw)
		}
	}
}

func TestDiscoveryProviderDoesNotInvokeSSH(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "ssh-was-called")
	t.Setenv("FIXTURE_SSH_MARKER", marker)
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\ntouch \"$FIXTURE_SSH_MARKER\"\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	dev := "#!/bin/sh\n[ \"$1 $2 $3\" = 'ssh list --json' ] || exit 2\nprintf '%s' '{\"schema_version\":1,\"kind\":\"ssh_list\",\"complete\":true,\"aliases\":[{\"name\":\"lab\",\"status\":\"active\",\"selectable\":true}]}'\n"
	if err := os.WriteFile(filepath.Join(bin, "dev"), []byte(dev), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	fixtureFile(t, filepath.Join(home, ".ssh", "config"), "Host lab\nMatch exec true\n")
	for _, provider := range []string{"ssh", "dev"} {
		inv, err := Discover(context.Background(), provider)
		if err != nil || len(inv.Candidates) != 1 {
			t.Fatal(provider, inv, err)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("discovery invoked SSH")
	}
	if _, err := Discover(context.Background(), "unexpected"); err == nil {
		t.Fatal("unknown provider accepted")
	}
}

func TestBoundedInventoryOutput(t *testing.T) {
	b := boundedBuffer{limit: 5}
	n, err := b.Write([]byte(strings.Repeat("x", 100)))
	if n != 100 || err != nil || !b.overflow || len(b.Bytes()) != 5 {
		t.Fatal(n, err, b)
	}
}
