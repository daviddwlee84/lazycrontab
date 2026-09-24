package document

import (
	"strings"
	"testing"

	"github.com/daviddwlee84/lazycrontab/internal/schedule"
)

func TestValidateRawEditAllowsOrdinaryNativeSource(t *testing.T) {
	raw := "# notes\r\n\nSHELL=/bin/sh\nPATH = '/usr/local/bin:/usr/bin'\nEMPTY=\n" +
		"  */5 9-17 * JAN,MAR MON-FRI printf '\\%s' '專案 # quoted'\\ \n" +
		"@reboot /bin/echo restart\n" +
		Marker + `{"v":1,"id":"stable","name":"保留 👩🏽‍💻"}` + "\n" + Disabled + "0 3 * * * echo disabled\n"
	warnings, err := ValidateRawEdit("", raw, schedule.System)
	if err != nil || len(warnings) != 0 {
		t.Fatal(warnings, err)
	}
}

func TestValidateRawEditReportsEveryMalformedNewLine(t *testing.T) {
	raw := "# heading\n*5 * * * * echo bad\n* * * * *\n@minutes echo typo\nPATH='unfinished\nnot a job\n"
	_, err := ValidateRawEdit("", raw, schedule.System)
	if err == nil {
		t.Fatal("accepted malformed source")
	}
	for _, line := range []string{"line 2:", "line 3:", "line 4:", "line 5:", "line 6:"} {
		if !strings.Contains(err.Error(), line) {
			t.Fatal("missing line diagnostic", line, err)
		}
	}
}

func TestValidateRawEditRetainsOnlyUnchangedUnknownLines(t *testing.T) {
	before := "unknown extension\n0 0 L * * echo unfamiliar\n0 9 * * * echo ordinary\n"
	after := "# moved entries\n0 10 * * * echo changed\n0 0 L * * echo unfamiliar\nunknown extension\n"
	warnings, err := ValidateRawEdit(before, after, schedule.System)
	if err != nil || len(warnings) != 2 || warnings[0].Line != 3 || warnings[1].Line != 4 {
		t.Fatal(warnings, err)
	}
	if _, err := ValidateRawEdit(before, after+"unknown extension\n", schedule.System); err == nil {
		t.Fatal("allowed another copy of unsupported line")
	}
	if _, err := ValidateRawEdit(before, strings.Replace(after, "echo unfamiliar", "echo modified", 1), schedule.System); err == nil {
		t.Fatal("allowed edited unsupported syntax")
	}
	if _, err := ValidateRawEdit(before, "# repaired\n0 0 1 * * echo familiar\n", schedule.System); err != nil {
		t.Fatal("repair was rejected", err)
	}
}

func TestValidateRawMetadataAndDuplicateRepair(t *testing.T) {
	meta := Marker + `{"v":1,"id":"same"}` + "\n"
	duplicate := meta + "* * * * * echo first\n" + meta + Disabled + "* * * * * echo second\n"
	if _, err := ValidateRawEdit(duplicate, duplicate, schedule.System); err == nil || !strings.Contains(err.Error(), "line 3: duplicate") {
		t.Fatal("existing duplicates were permitted", err)
	}
	repaired := strings.Replace(duplicate, meta+Disabled, Marker+`{"v":1,"id":"second"}`+"\n"+Disabled, 1)
	if _, err := ValidateRawEdit(duplicate, repaired, schedule.System); err != nil {
		t.Fatal("duplicate repair rejected", err)
	}
	for _, malformed := range []string{
		Marker + "not json\n* * * * * echo hi\n",
		Marker + `{"v":1,"id":""}` + "\n* * * * * echo hi\n",
		Marker + `{"v":2,"id":"unknown-version"}` + "\n* * * * * echo hi\n",
		meta + "# detached metadata\n* * * * * echo hi\n",
		strings.TrimSpace(Disabled) + "\n",
	} {
		if _, err := ValidateRawEdit("", malformed, schedule.System); err == nil {
			t.Fatalf("accepted new malformed metadata: %q", malformed)
		}
		warnings, err := ValidateRawEdit(malformed, malformed, schedule.System)
		if err != nil || len(warnings) == 0 {
			t.Fatal("unchanged unsupported metadata should remain repairable", malformed, warnings, err)
		}
	}
}

func TestValidateRawSupercronicFieldsAndEnvironment(t *testing.T) {
	raw := "CRON_TZ=UTC\nEMPTY=\"\"\nSHELL=/bin/sh\n" +
		"0 9 * * * echo five\n0 9 * * * 2030 echo six\n*/5 * * * * * * echo seven\n@hourly echo macro\n"
	if warnings, err := ValidateRawEdit("", raw, schedule.Supercronic); err != nil || len(warnings) != 0 {
		t.Fatal(warnings, err)
	}
	for _, invalid := range []string{"@reboot echo unsupported\n", "CRON_TZ=Not/AZone\n", "EMPTY=\n", "*5 * * * * echo bad\n", "# " + strings.Repeat("x", 64*1024) + "\n"} {
		if _, err := ValidateRawEdit("", invalid, schedule.Supercronic); err == nil {
			t.Fatalf("accepted invalid Supercronic input: %.80q", invalid)
		}
	}
	// Do not mistake a numeric first command word for a year when it cannot be
	// parsed as one. This follows the pinned upstream longest-valid-field rule.
	if _, err := ValidateRawEdit("", "* * * * * 999999 argument\n", schedule.Supercronic); err != nil {
		t.Fatal(err)
	}
}

func FuzzValidateRawEdit(f *testing.F) {
	f.Add("* * * * * echo ordinary\n")
	f.Add("*/5 * * * * * * echo seconds\n")
	f.Add(Marker + `{"v":1,"id":"stable"}` + "\n@hourly echo macro\n")
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 65536 {
			return
		}
		for _, dialect := range []schedule.Dialect{schedule.System, schedule.Supercronic} {
			ValidateRawEdit("", raw, dialect)
			ValidateRawEdit(raw, raw, dialect)
		}
	})
}
