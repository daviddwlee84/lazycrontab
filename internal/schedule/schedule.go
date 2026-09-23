// Package schedule adapts expression libraries to the selected cron dialect.
// It never starts a scheduler or executes a job.
package schedule

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aptible/supercronic/cronexpr"
	descriptor "github.com/lnquy/cron"
	cron "github.com/robfig/cron/v3"
)

type Dialect string

const (
	System      Dialect = "system"
	Supercronic Dialect = "supercronic"
)

type Schedule struct {
	Expression  string   `json:"expression"`
	Dialect     Dialect  `json:"dialect"`
	Description string   `json:"description"`
	Event       bool     `json:"event"`
	Warnings    []string `json:"warnings,omitempty"`
	next        func(time.Time) time.Time
}

var macros = map[string]string{"@annually": "0 0 1 1 *", "@yearly": "0 0 1 1 *", "@monthly": "0 0 1 * *", "@weekly": "0 0 * * 0", "@daily": "0 0 * * *", "@midnight": "0 0 * * *", "@hourly": "0 * * * *"}

func Parse(expr string, dialect Dialect, loc *time.Location, locale string) (result *Schedule, parseErr error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			parseErr = fmt.Errorf("invalid cron expression: %v", r)
		}
	}()
	if dialect == "" {
		dialect = System
	}
	if dialect != System && dialect != Supercronic {
		return nil, fmt.Errorf("unknown cron dialect %q", dialect)
	}
	if loc == nil {
		return nil, fmt.Errorf("schedule timezone is unknown; set source timezone")
	}
	expr = strings.TrimSpace(expr)
	s := &Schedule{Expression: expr, Dialect: dialect, Warnings: []string{}}
	if expr == "@reboot" && dialect == System {
		s.Event = true
		s.Description = "When the cron daemon starts"
		return s, nil
	}
	fieldsExpr := expr
	if m, ok := macros[expr]; ok {
		fieldsExpr = m
	}
	f := strings.Fields(fieldsExpr)
	if dialect == System {
		if len(f) != 5 {
			return nil, fmt.Errorf("system cron requires five fields or a supported @macro")
		}
		for _, v := range f {
			if strings.ContainsAny(v, "?#~=") || strings.HasPrefix(v, ",") || strings.HasSuffix(v, ",") || strings.Contains(v, ",,") {
				return nil, fmt.Errorf("%q is not supported by the system cron profile", v)
			}
		}
		// robfig's DOW range is 0..6. Expand numeric 0..7 ranges before
		// mapping 7 to 0; replacing 7 textually would break 5-7 and */2.
		dow, err := normalizeDOW(f[4])
		if err != nil {
			return nil, err
		}
		normalized := append([]string(nil), f...)
		normalized[4] = dow
		p := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		parsed, err := p.Parse(strings.Join(normalized, " "))
		if err != nil {
			return nil, err
		}
		sp := parsed.(*cron.SpecSchedule)
		sp.Location = loc
		// Vixie/Cronie track leading wildcard syntax, including */N.
		const star uint64 = 1 << 63
		sp.Dom &^= star
		sp.Dow &^= star
		if strings.HasPrefix(f[2], "*") {
			sp.Dom |= star
		}
		if strings.HasPrefix(f[4], "*") {
			sp.Dow |= star
		}
		s.next = sp.Next
	} else {
		if len(f) != 5 && len(f) != 6 && len(f) != 7 {
			return nil, fmt.Errorf("Supercronic requires 5 fields, 6 with year, or 7 with seconds and year")
		}
		parsed, err := cronexpr.ParseStrict(expr)
		if err != nil {
			return nil, err
		}
		s.next = func(t time.Time) time.Time { return parsed.Next(t.In(loc)) }
		if len(f) == 6 {
			f = append([]string{"0"}, f...)
		}
	}
	// Normalize macros before describing; validation always precedes prose.
	d, err := descriptor.NewDescriptor(descriptor.Use24HourTimeFormat(true), descriptor.SetLocales(descriptor.Locale_en, descriptor.Locale_zh_TW))
	if err == nil {
		lang := descriptor.Locale_en
		if locale == "zh_TW" {
			lang = descriptor.Locale_zh_TW
		}
		s.Description, err = d.ToDescription(strings.Join(f, " "), lang)
	}
	if err != nil || s.Description == "" {
		s.Description = "Cron: " + expr
	}
	dom, dow := 2, 4
	if len(f) == 7 {
		dom, dow = 3, 5
	}
	if !strings.HasPrefix(f[dom], "*") && !strings.HasPrefix(f[dow], "*") {
		// Descriptor ports commonly describe the two day constraints as AND.
		left, right := append([]string(nil), f...), append([]string(nil), f...)
		left[dow] = "*"
		right[dom] = "*"
		lang := descriptor.Locale_en
		connector := " OR "
		if locale == "zh_TW" {
			lang = descriptor.Locale_zh_TW
			connector = " 或 "
		}
		ld, le := d.ToDescription(strings.Join(left, " "), lang)
		rd, re := d.ToDescription(strings.Join(right, " "), lang)
		if le == nil && re == nil {
			s.Description = ld + connector + rd
		} else {
			s.Description = "Cron day constraints use OR: " + expr
		}
		s.Warnings = append(s.Warnings, "Both day fields are restricted: either matching day triggers the job.")
	}
	if len(f) == 5 && (f[2] == "29" || f[2] == "30" || f[2] == "31") {
		s.Warnings = append(s.Warnings, "Months without this day are skipped.")
	}
	return s, nil
}

func normalizeDOW(v string) (string, error) {
	names := map[string]string{"sun": "0", "mon": "1", "tue": "2", "wed": "3", "thu": "4", "fri": "5", "sat": "6"}
	v = strings.ToLower(v)
	for name, n := range names {
		v = strings.ReplaceAll(v, name, n)
	}
	var parts []string
	for _, p := range strings.Split(v, ",") {
		step := 1
		base := p
		if strings.Contains(p, "/") {
			a := strings.Split(p, "/")
			if len(a) != 2 {
				return "", fmt.Errorf("invalid weekday %q", v)
			}
			base = a[0]
			n, e := strconv.Atoi(a[1])
			if e != nil || n < 1 || n > 1000 {
				return "", fmt.Errorf("invalid weekday step")
			}
			step = n
		}
		lo, hi := 0, 7
		if base != "*" {
			r := strings.Split(base, "-")
			if len(r) > 2 {
				return "", fmt.Errorf("invalid weekday")
			}
			n, e := strconv.Atoi(r[0])
			if e != nil {
				return "", fmt.Errorf("invalid weekday %q", base)
			}
			lo, hi = n, n
			if len(r) == 2 {
				hi, e = strconv.Atoi(r[1])
				if e != nil {
					return "", e
				}
			} else if strings.Contains(p, "/") {
				hi = 7
			}
		}
		if lo < 0 || hi > 7 || lo > hi {
			return "", fmt.Errorf("weekday must be 0..7")
		}
		for n := lo; n <= hi; n += step {
			parts = append(parts, strconv.Itoa(n%7))
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("empty weekday")
	}
	return strings.Join(parts, ","), nil
}

func (s *Schedule) Next(t time.Time) time.Time {
	if s.Event || s.next == nil {
		return time.Time{}
	}
	return s.next(t)
}
func (s *Schedule) NextN(ctx context.Context, from time.Time, n int) ([]time.Time, error) {
	if n < 1 || n > 1000 {
		return nil, fmt.Errorf("count must be between 1 and 1000")
	}
	out := []time.Time{}
	for range n {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		next := s.Next(from)
		if next.IsZero() {
			break
		}
		if !next.After(from) {
			return out, fmt.Errorf("parser did not advance")
		}
		out = append(out, next)
		from = next
	}
	return out, nil
}

var intervalRE = regexp.MustCompile(`^every (\d+) (minutes?|hours?)$`)
var atRE = regexp.MustCompile(`^(daily|every day|every weekday|every (monday|tuesday|wednesday|thursday|friday|saturday|sunday)) at (\d{1,2}):(\d{2})$`)

func FromHuman(input string) (string, error) {
	s := strings.ToLower(strings.Join(strings.Fields(input), " "))
	if s == "hourly" || s == "every hour" {
		return "0 * * * *", nil
	}
	if s == "every minute" {
		return "* * * * *", nil
	}
	if m := intervalRE.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		limit := 60
		if strings.HasPrefix(m[2], "hour") {
			limit = 24
		}
		if n < 1 || n > limit || limit%n != 0 {
			return "", fmt.Errorf("%q cannot be expressed as an exact repeating interval in five-field cron; use explicit field slots instead", input)
		}
		if limit == 60 {
			if n == 60 {
				return "0 * * * *", nil
			}
			return fmt.Sprintf("*/%d * * * *", n), nil
		}
		if n == 24 {
			return "0 0 * * *", nil
		}
		return fmt.Sprintf("0 */%d * * *", n), nil
	}
	if m := atRE.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[3])
		min, _ := strconv.Atoi(m[4])
		if h > 23 || min > 59 {
			return "", fmt.Errorf("time must use HH:MM (00:00–23:59)")
		}
		dow := "*"
		if m[1] == "every weekday" {
			dow = "1-5"
		}
		if m[2] != "" {
			dow = map[string]string{"sunday": "0", "monday": "1", "tuesday": "2", "wednesday": "3", "thursday": "4", "friday": "5", "saturday": "6"}[m[2]]
		}
		return fmt.Sprintf("%d %d * * %s", min, h, dow), nil
	}
	return "", fmt.Errorf("unsupported phrase; try 'every 15 minutes', 'daily at 03:00', or 'every weekday at 09:00'")
}
