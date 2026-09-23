package schedule

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSystemCompatibility(t *testing.T) {
	for _, expr := range []string{"* * * * *", "0 9 * * MON-FRI", "0 0 * * 7", "0 0 * * 5-7", "0 0 * * */2", "0 0 1 JUL WED", "@daily", "@reboot", "0 0 29 2 *"} {
		if _, e := Parse(expr, System, time.UTC, "en"); e != nil {
			t.Errorf("%s: %v", expr, e)
		}
	}
	for _, expr := range []string{"@every 1h", "* * * * ?", "0 0 L * *", "0 0 * * 8", "0 0 * * 7-2", "0 0 * * 1,,2", "0 0 0 * * *", "TZ=Europe/London", "61 * * * *", "*/0 * * * *"} {
		if _, e := Parse(expr, System, time.UTC, "en"); e == nil {
			t.Errorf("accepted %q", expr)
		}
	}
	s, _ := Parse("* * * * *", System, time.UTC, "en")
	if s.Description != "Every minute" {
		t.Fatalf("unexpected description %q", s.Description)
	}
}
func TestSundayRangesAndDayOR(t *testing.T) {
	from := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	for _, expr := range []string{"0 0 * * 7", "0 0 * * SUN"} {
		s, _ := Parse(expr, System, time.UTC, "en")
		if n := s.Next(from); n.Weekday() != time.Sunday {
			t.Fatal(n)
		}
	}
	s, _ := Parse("0 0 1 * 1", System, time.UTC, "en")
	next, _ := s.NextN(context.Background(), from, 3)
	want := []int{28, 1, 5}
	for i, d := range want {
		if next[i].Day() != d {
			t.Errorf("OR: %v", next)
		}
	}
	if !strings.Contains(s.Description, "OR") {
		t.Fatal(s.Description)
	}
	s, _ = Parse("0 0 */2 * 1", System, time.UTC, "en")
	next, _ = s.NextN(context.Background(), from, 2)
	for _, n := range next {
		if n.Weekday() != time.Monday || n.Day()%2 != 1 {
			t.Fatalf("leading wildcard must retain Vixie semantics: %s", n)
		}
	}
}
func TestORDescriptionKeepsMonthConstraint(t *testing.T) {
	s, e := Parse("0 0 1 JAN MON", System, time.UTC, "en")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(s.Description, "OR") || strings.Count(s.Description, "January") != 2 {
		t.Fatal(s.Description)
	}
}
func TestSupercronicFieldOrder(t *testing.T) {
	from := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	s, e := Parse("0 0 1 1 * 2028", Supercronic, time.UTC, "en")
	if e != nil {
		t.Fatal(e)
	}
	if n := s.Next(from); n.Year() != 2028 || n.Month() != 1 || n.Day() != 1 {
		t.Fatal(n)
	}
	s, e = Parse("*/2 * * * * * *", Supercronic, time.UTC, "en")
	if e != nil {
		t.Fatal(e)
	}
	if n := s.Next(from); n.Sub(from) != 2*time.Second {
		t.Fatal(n)
	}
	if _, e = Parse("@reboot", Supercronic, time.UTC, "en"); e == nil {
		t.Fatal("supercronic does not implement @reboot")
	}
}
func TestDSTAndMissingDate(t *testing.T) {
	loc, e := time.LoadLocation("America/New_York")
	if e != nil {
		t.Fatal(e)
	}
	s, _ := Parse("30 2 * * *", System, loc, "en")
	from := time.Date(2026, 3, 7, 3, 0, 0, 0, loc)
	n := s.Next(from)
	if n.Day() != 9 || n.Hour() != 2 {
		t.Fatalf("DST gap: %v", n)
	}
	s, _ = Parse("30 1 * * *", System, loc, "en")
	from = time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	runs, _ := s.NextN(context.Background(), from, 2)
	if len(runs) != 2 || runs[1].Sub(runs[0]) != time.Hour {
		t.Fatalf("DST repeated hour: %v", runs)
	}
	s, _ = Parse("0 0 30 2 *", System, time.UTC, "en")
	if !s.Next(time.Now()).IsZero() {
		t.Fatal("February 30 must not run")
	}
}
func TestHumanRepresentability(t *testing.T) {
	valid := map[string]string{"every 15 minutes": "*/15 * * * *", "daily at 03:00": "0 3 * * *", "every weekday at 09:00": "0 9 * * 1-5", "every monday at 08:30": "30 8 * * 1", "every 24 hours": "0 0 * * *"}
	for phrase, want := range valid {
		got, e := FromHuman(phrase)
		if e != nil || got != want {
			t.Errorf("%s: %s %v", phrase, got, e)
		}
	}
	for _, phrase := range []string{"every 90 minutes", "every 7 minutes", "every 5 hours", "every other Friday at 08:00", "daily at 24:00", "every 0 minutes"} {
		if _, e := FromHuman(phrase); e == nil {
			t.Errorf("approximated %q", phrase)
		}
	}
}
func FuzzParseNeverPanics(f *testing.F) {
	for _, seed := range []string{"", "TZ=", "* * * * *", "@reboot", "0 0 * * 7", "#", "0 0 1 1 * 2028"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, expr string) {
		if len(expr) > 4096 {
			return
		}
		_, _ = Parse(expr, System, time.UTC, "en")
		_, _ = Parse(expr, Supercronic, time.UTC, "en")
	})
}
