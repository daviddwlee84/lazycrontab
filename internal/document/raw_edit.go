package document

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/schedule"
)

// RawIssue locates a validation error or preserved unsupported line in the
// proposed document. Validation never renders jobs or rewrites source bytes.
type RawIssue struct {
	Line    int
	Message string
}

func (i RawIssue) String() string { return fmt.Sprintf("line %d: %s", i.Line, i.Message) }

type rawLine struct {
	body       string
	issue      string
	metadataID string
	metadata   bool
	job        bool
}

var supercronicAssignment = regexp.MustCompile(`^([^\s=]+)\s*=\s*(.*)$`)

// ValidateRawEdit permits existing unsupported lines only while their bytes and
// multiplicity remain unchanged. Moving such a line is allowed; copying or
// changing it requires syntax understood by the selected source profile.
// Duplicate managed IDs always require repair rather than being carried over.
func ValidateRawEdit(before, after string, dialect schedule.Dialect) (warnings []RawIssue, err error) {
	if dialect != schedule.System && dialect != schedule.Supercronic {
		return nil, fmt.Errorf("unknown cron dialect %q", dialect)
	}
	old := scanRawLines(before, dialect)
	newLines := scanRawLines(after, dialect)
	unchanged := map[string]int{}
	for _, line := range old {
		if line.issue != "" {
			unchanged[line.body]++
		}
	}
	seen := map[string]int{}
	errors := []string{}
	for i, line := range newLines {
		if line.metadataID != "" {
			if first, exists := seen[line.metadataID]; exists {
				errors = append(errors, RawIssue{i + 1, fmt.Sprintf("duplicate lazycrontab ID %q (first used on line %d); repair or remove its metadata", line.metadataID, first)}.String())
			} else {
				seen[line.metadataID] = i + 1
			}
		}
		if line.issue == "" {
			continue
		}
		if unchanged[line.body] > 0 {
			unchanged[line.body]--
			warnings = append(warnings, RawIssue{i + 1, "preserved existing unsupported syntax: " + line.issue})
		} else {
			errors = append(errors, RawIssue{i + 1, line.issue}.String())
		}
	}
	if len(errors) != 0 {
		return warnings, fmt.Errorf("invalid crontab edit:\n%s", strings.Join(errors, "\n"))
	}
	return warnings, nil
}

func scanRawLines(raw string, dialect schedule.Dialect) []rawLine {
	bodies := strings.Split(raw, "\n")
	lines := make([]rawLine, len(bodies))
	for i, body := range bodies {
		line := rawLine{body: body}
		// CRLF is kept byte-for-byte; only a stray embedded CR is invalid.
		text := strings.TrimSuffix(body, "\r")
		trim := strings.TrimLeft(text, " \t")
		switch {
		case strings.ContainsAny(text, "\r\x00"):
			line.issue = "cron entries cannot contain NUL or embedded carriage returns"
		case strings.HasPrefix(trim, strings.TrimSpace(Marker)):
			line.metadata = true
			var metadata Metadata
			if !strings.HasPrefix(trim, Marker) || json.Unmarshal([]byte(strings.TrimPrefix(trim, Marker)), &metadata) != nil {
				line.issue = "invalid lazycrontab metadata JSON; restore the comment or remove it to keep an unmanaged job"
			} else if metadata.Version != 1 {
				line.issue = fmt.Sprintf("unsupported lazycrontab metadata version %d", metadata.Version)
			} else if metadata.ID == "" || strings.ContainsAny(metadata.ID, "\r\n\x00") {
				line.issue = "lazycrontab metadata requires a nonempty single-line ID"
			} else {
				line.metadataID = metadata.ID
			}
		case strings.HasPrefix(trim, Disabled):
			line.job, line.issue = validateRawJob(strings.TrimPrefix(trim, Disabled), dialect)
		case strings.HasPrefix(trim, strings.TrimSpace(Disabled)):
			line.issue = "disabled job marker requires a cron expression and command"
		case strings.HasPrefix(trim, "#") || strings.TrimSpace(trim) == "":
			// Ordinary comments and blank lines have no execution semantics.
		default:
			matcher := assignment
			if dialect == schedule.Supercronic {
				matcher = supercronicAssignment
			}
			if match := matcher.FindStringSubmatch(trim); match != nil {
				line.issue = validateRawAssignment(match[1], match[2], dialect)
			} else {
				line.job, line.issue = validateRawJob(trim, dialect)
			}
		}
		if dialect == schedule.Supercronic && len(body) >= 64*1024 {
			line.issue = "line exceeds Supercronic's 64 KiB scanner limit; move long commands into a script"
		}
		lines[i] = line
	}
	for i := range lines {
		if lines[i].metadata && lines[i].issue == "" && (i+1 == len(lines) || !lines[i+1].job) {
			lines[i].issue = "lazycrontab metadata must be immediately followed by a cron job or managed disabled job"
		}
	}
	return lines
}

func validateRawAssignment(key, value string, dialect schedule.Dialect) string {
	value = strings.TrimSpace(value)
	if dialect == schedule.Supercronic {
		// The pinned upstream parser indexes envVal[0]; an empty unquoted
		// value panics, while NAME="" is its supported empty-value syntax.
		if value == "" {
			return "Supercronic requires an explicit quoted empty value, for example " + key + "=\"\""
		}
	} else if strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "'") {
		if len(value) < 2 || value[len(value)-1] != value[0] {
			return "environment assignment has an unterminated quoted value"
		}
	}
	if len(value) >= 2 && (value[0] == '\'' || value[0] == '"') && value[len(value)-1] == value[0] {
		value = value[1 : len(value)-1]
	}
	if key == "CRON_TZ" && dialect == schedule.Supercronic {
		if _, err := time.LoadLocation(value); err != nil {
			return "invalid CRON_TZ timezone: " + err.Error()
		}
	}
	return ""
}

func validateRawJob(text string, dialect schedule.Dialect) (job bool, issue string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			job = false
			issue = fmt.Sprintf("invalid cron expression: %v", recovered)
		}
	}()
	j, ok := parseJob(text, dialect, false)
	if !ok {
		if dialect == schedule.Supercronic {
			return false, "expected a valid Supercronic expression (5 fields, 6 with year, or 7 with seconds and year) followed by a command"
		}
		return false, "expected five cron fields or a supported @macro followed by a nonempty command"
	}
	if _, err := schedule.Parse(j.Schedule, dialect, time.UTC, "en"); err != nil {
		return true, "invalid schedule: " + err.Error()
	}
	return true, ""
}
