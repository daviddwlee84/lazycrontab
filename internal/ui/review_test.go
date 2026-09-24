package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestReviewBodyKeepsStructuredDiffForLinearOutput(t *testing.T) {
	for _, test := range []struct {
		review Review
		want   string
	}{{Review{Text: "summary"}, "summary"}, {Review{Diff: "diff\n"}, "diff\n"}, {Review{Text: "summary\n", Diff: "diff\n"}, "summary\n\ndiff\n"}} {
		if got := test.review.Body(); got != test.want {
			t.Fatal(got, test.want)
		}
	}
}

func modalFixture(t *testing.T) (*dashboard, *Form, *int) {
	t.Helper()
	m := fixtureDashboard(t)
	m.mouse = true
	m.filter.SetValue("second")
	m.clamp()
	applied := new(int)
	f := NewReviewForm(m.ctx, FormSpec{Title: "Disable job · local/user", Mouse: true, Build: func(context.Context, map[string]string) (Review, error) {
		return Review{Text: "Job: second\nStatus: enabled → disabled", Diff: "--- current\n+++ proposed\n-* * * * * echo hi\n+# lazycrontab-disabled: * * * * * echo hi\n"}, nil
	}, Apply: func(context.Context, map[string]string, Review) (string, error) {
		*applied++
		return "Saved · local/user\nJob disabled\nBackup retained", nil
	}})
	f.SetEmbedded(true)
	m.child, m.childView = f, "jobs"
	f.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height - 2})
	t.Cleanup(f.cancel)
	return m, f, applied
}

func TestReviewPopupKeepsDashboardAndOwnsAllInputs(t *testing.T) {
	m, f, applied := modalFixture(t)
	m.Update(f.reviewCmd()())
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 44})
	view := ansi.Strip(m.View().Content)
	for _, expected := range []string{"Hosts / sources", "Jobs [1]", "/ second", "Disable job", "Changes", "--- current", "+++ proposed", "Apply ^S"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("popup lost %q\n%s", expected, view)
		}
	}
	selected := m.selectedKey
	for _, msg := range []tea.Msg{
		key('2'), tea.KeyPressMsg{Code: '3', Mod: tea.ModAlt}, tea.PasteMsg{Content: "y\n"},
		tea.MouseClickMsg{X: 29, Y: 0}, tea.MouseReleaseMsg{X: 29, Y: 0},
		tea.MouseClickMsg{X: 1, Y: 6}, tea.MouseReleaseMsg{X: 1, Y: 6},
	} {
		m.Update(msg)
	}
	if m.view != "jobs" || m.selectedKey != selected || m.filter.Value() != "second" || *applied != 0 || f.stage != "review" {
		t.Fatal("modal leaked input", m.view, m.selectedKey, m.filter.Value(), *applied, f.stage)
	}
	if _, cmd := m.Update(special(tea.KeyEnter)); cmd != nil || *applied != 0 {
		t.Fatal("Enter applied a review")
	}
	_, apply := m.Update(ctrl('s'))
	if apply == nil || f.stage != "applying" {
		t.Fatal("explicit apply was not dispatched")
	}
	if _, again := m.Update(ctrl('s')); again != nil {
		t.Fatal("second key dispatched duplicate write")
	}
	m.Update(apply())
	if *applied != 1 || f.stage != "result" || m.child != f || !strings.Contains(m.View().Content, "Job disabled") {
		t.Fatal("result did not remain open")
	}
	m.Update(tea.KeyPressMsg{Code: '2', Mod: tea.ModAlt})
	if m.view != "jobs" || m.child != f {
		t.Fatal("result allowed view switching before acknowledgement")
	}
	_, ack := m.Update(special(tea.KeyEnter))
	if ack == nil {
		t.Fatal("result acknowledgement missing")
	}
	done := ack().(WorkflowDoneMsg)
	if !done.Changed || done.Owner != f {
		t.Fatal(done)
	}
	m.Update(done)
	if m.child != nil || m.selectedKey != selected || m.filter.Value() != "second" {
		t.Fatal("acknowledgement lost dashboard context")
	}
}

func TestReviewPopupBuildingFailureAndLateOwnership(t *testing.T) {
	m, f, _ := modalFixture(t)
	build := f.reviewCmd()
	if !f.Modal() || !strings.Contains(m.View().Content, "Preparing review") {
		t.Fatal("building became a replacement page")
	}
	m.Update(tea.KeyPressMsg{Code: '2', Mod: tea.ModAlt})
	if m.view != "jobs" {
		t.Fatal("building leaked keys")
	}
	_, cancel := m.Update(special(tea.KeyEscape))
	m.Update(cancel())
	late := build()
	m.Update(late)
	f.Update(late)
	if m.child != nil || f.stage == "review" {
		t.Fatal("cancelled review reopened")
	}
	other := NewReviewForm(context.Background(), FormSpec{Title: "failure"})
	defer other.cancel()
	other.SetEmbedded(true)
	other.stage = "building"
	other.Update(formBuilt{owner: f, review: Review{Text: "stale"}})
	if other.stage != "building" {
		t.Fatal("foreign review accepted")
	}
	other.Update(formBuilt{owner: other, err: errors.New("source became unavailable")})
	if other.stage != "result" || !other.Modal() || !strings.Contains(other.View().Content, "No write was sent") || !strings.Contains(other.View().Content, "source became unavailable") {
		t.Fatal("fieldless build failure did not stay in the popup", other.View().Content)
	}
	_, ack := other.Update(special(tea.KeyEnter))
	if done := ack().(WorkflowDoneMsg); done.Changed || done.Err == nil {
		t.Fatal("build failure claimed a write", done)
	}
}

func TestReviewPopupScrollResizeAndMouseGeometry(t *testing.T) {
	m, f, applied := modalFixture(t)
	f.review = Review{Text: strings.Repeat("Long review row 專案 é 👩🏽‍💻\n", 90), Diff: "--- current\n+++ proposed\n-" + strings.Repeat("same ", 160) + "before\n+" + strings.Repeat("same ", 160) + "after\n"}
	f.stage = "review"
	for _, size := range [][2]int{{180, 44}, {120, 40}, {80, 24}, {40, 12}, {10, 4}, {1, 1}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := m.View().Content
		lines := strings.Split(view, "\n")
		if len(lines) > size[1] {
			t.Fatal("height overflow", size)
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("width overflow %v: %q", size, line)
			}
		}
		for _, hit := range f.hits() {
			if hit.rect.y+2 >= len(lines) {
				t.Fatal("offscreen hit", size, hit)
			}
			if hit.id == "apply" && !strings.Contains(ansi.Strip(lines[hit.rect.y+2]), "Apply") {
				t.Fatal("invisible apply is clickable", size, hit)
			}
		}
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.Update(special(tea.KeyPgDown))
	if f.scroll == 0 {
		t.Fatal("page down did not scroll")
	}
	m.Update(special(tea.KeyEnd))
	if !strings.Contains(ansi.Strip(m.View().Content), "after") {
		t.Fatal("long diff tail was truncated")
	}
	end := f.scroll
	m.Update(tea.MouseWheelMsg{X: 1, Y: 1, Button: tea.MouseWheelDown})
	if f.scroll != end {
		t.Fatal("scroll exceeded content")
	}
	m.Update(tea.MouseWheelMsg{X: 1, Y: 1, Button: tea.MouseWheelUp})
	if f.scroll >= end {
		t.Fatal("mouse scroll did not move modal")
	}
	var apply hitRegion
	for _, hit := range f.hits() {
		if hit.id == "apply" {
			apply = hit
		}
	}
	m.Update(tea.MouseClickMsg{X: apply.rect.x, Y: apply.rect.y + 2})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if _, cmd := m.Update(tea.MouseReleaseMsg{X: apply.rect.x, Y: apply.rect.y + 2}); cmd != nil || *applied != 0 {
		t.Fatal("resize retained mouse write authority")
	}
	m.Update(tea.MouseClickMsg{X: apply.rect.x, Y: apply.rect.y + 2})
	_, cmd := m.Update(tea.MouseReleaseMsg{X: apply.rect.x, Y: apply.rect.y + 2})
	if cmd == nil {
		t.Fatal("visible apply button not clickable")
	}
	m.Update(cmd())
	if *applied != 1 {
		t.Fatal("mouse apply did not execute exactly once")
	}
}

func TestResultPopupRetainsFullFailureAndWaitsForCancelledWrite(t *testing.T) {
	m, f, _ := modalFixture(t)
	f.stage = "applying"
	f.applyDispatched = true
	m.Update(special(tea.KeyEscape))
	if m.child != f || !f.cancelRequested || f.stage != "applying" {
		t.Fatal("cancelled a dispatched write without awaiting outcome")
	}
	message := "Target: local/user\n" + strings.Repeat("Recovery detail\n", 80) + "Final recovery instruction"
	m.Update(formApplied{owner: f, message: "No confirmed save", err: fmt.Errorf("%s", message)})
	if f.stage != "result" || !strings.Contains(m.View().Content, "Operation failed") {
		t.Fatal("failure did not remain in popup")
	}
	m.Update(special(tea.KeyEnd))
	if !strings.Contains(m.View().Content, "Final recovery instruction") {
		t.Fatal("error tail was clipped rather than scrollable")
	}
	_, ack := m.Update(special(tea.KeyEnter))
	if done := ack().(WorkflowDoneMsg); !done.Changed || done.Err == nil {
		t.Fatal("unknown write did not request reconciliation", done)
	}
}

func TestDiffRenderingPreservesWrappedUnicodeAndEmphasizesChanges(t *testing.T) {
	content := strings.Repeat("專案 é 👩🏽‍💻 echo 'literal%'; ", 20)
	for _, width := range []int{12, 34, 100} {
		lines := renderReviewDiff("--- current\n+++ proposed\n-"+content+"before\n+"+content+"after\n", width, true)
		var removed, added strings.Builder
		kind := byte(0)
		for _, styled := range lines {
			if ansi.StringWidth(styled) > width {
				t.Fatalf("diff overflow width %d: %q", width, styled)
			}
			line := ansi.Strip(styled)
			if strings.HasPrefix(line, "- ") {
				kind = '-'
			} else if strings.HasPrefix(line, "+ ") {
				kind = '+'
			} else if !strings.HasPrefix(line, "│ ") {
				continue
			}
			body := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(line, "- "), "+ "), "│ ")
			if kind == '-' {
				removed.WriteString(body)
			} else {
				added.WriteString(body)
			}
		}
		if removed.String() != content+"before" || added.String() != content+"after" {
			t.Fatal("wrapping dropped a grapheme or space", width, removed.String(), added.String())
		}
	}
	styled := emphasizeDifference("prefix 專案 before suffix", "prefix 專案 after suffix", lipgloss.NewStyle())
	if ansi.Strip(styled) != "prefix 專案 before suffix" || !strings.Contains(styled, "\x1b[") {
		t.Fatal("inline emphasis lost content or style", styled)
	}
}

func TestOverlayCellPlacementDoesNotShiftAtWideGraphemes(t *testing.T) {
	background := strings.Repeat("專案👩🏽‍💻", 12)
	for x := 0; x < 12; x++ {
		got := ansi.Strip(overlayText(background, "[ panel ]", x, 0, 60, 1))
		if ansi.StringWidth(got) != 60 || ansi.Cut(got, x, x+9) != "[ panel ]" {
			t.Fatal("overlay shifted at a grapheme boundary", x, got)
		}
	}
}

func TestReviewDiffNoColorRetainsTextualChangeMarkers(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	got := strings.Join(renderReviewDiff("--- current\n+++ proposed\n-echo before\n+echo after\n", 80, true), "\n")
	for _, color := range []string{"[38;", "[48;", "[31m", "[32m"} {
		if strings.Contains(got, color) {
			t.Fatal("NO_COLOR diff contains color", got)
		}
	}
	plain := ansi.Strip(got)
	if !strings.Contains(plain, "- echo before") || !strings.Contains(plain, "+ echo after") {
		t.Fatal("plain diff lost change markers", plain)
	}
}

func TestResultAcknowledgementCompactsReceiptWithoutDisplacingFooter(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint("failure=", failure), func(t *testing.T) {
			m, f, _ := modalFixture(t)
			m.Update(tea.WindowSizeMsg{Width: 180, Height: 44})
			f.stage = "applying"
			f.applyDispatched = true
			message := "Saved · local/user\nJob disabled\nBackup: /private/long/path\nAdditional receipt detail"
			var err error
			want := "Saved · local/user"
			if failure {
				want = "Save could not be confirmed"
				err = errors.New(want + "\nRecovery details stay in the popup")
			}
			m.Update(formApplied{owner: f, message: message, err: err})
			if !strings.Contains(ansi.Strip(m.View().Content), "Additional receipt detail") {
				t.Fatal("popup lost full receipt")
			}
			_, ack := m.Update(special(tea.KeyEnter))
			m.Update(ack())
			if m.status != want {
				t.Fatal("acknowledgement retained multiline status", m.status)
			}
			lines := strings.Split(ansi.Strip(m.View().Content), "\n")
			if len(lines) != m.height || !strings.Contains(lines[m.height-3], want) || !strings.Contains(lines[m.height-2], "Actions :") || !strings.Contains(lines[m.height-1], "Tab focus") {
				t.Fatal("receipt displaced footer actions", strings.Join(lines[m.height-3:], "\n"))
			}
			if strings.Contains(strings.Join(lines[m.height-3:], "\n"), "Backup:") {
				t.Fatal("full receipt leaked into single-line status")
			}
		})
	}
}

func TestDashboardFooterContainsMultilineExternalStatus(t *testing.T) {
	m := fixtureDashboard(t)
	m.status = "\n\x1b[31mConnection failed\x1b[0m\nRemote diagnostic\nMore details"
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	if !strings.Contains(lines[m.height-3], "Connection failed") || !strings.Contains(lines[m.height-1], "Tab focus") || strings.Contains(lines[m.height-2], "Remote diagnostic") {
		t.Fatal("multiline diagnostic overwrote footer", strings.Join(lines[m.height-3:], "\n"))
	}
}

func TestResultHeadingUsesOutcomeStyleWithoutParsingReceipt(t *testing.T) {
	for _, failed := range []bool{false, true} {
		f := NewForm(context.Background(), FormSpec{})
		f.stage = "result"
		f.message = "Operation summary\nDetails remain plain"
		style := styles(f.dark).Success.Bold(true)
		if failed {
			f.err = errors.New("failed")
			style = styles(f.dark).Error.Bold(true)
		}
		lines := f.reviewLines()
		if lines[0] != style.Render("Operation summary") || lines[1] != "Details remain plain" {
			t.Fatal(failed, lines)
		}
		f.cancel()
	}
}
