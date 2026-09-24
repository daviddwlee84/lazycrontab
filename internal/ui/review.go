package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type reviewLayout struct {
	box, body rect
	hits      []hitRegion
}

type reviewPresentation struct {
	key   reviewPresentationKey
	lines []string
}

type reviewPresentationKey struct {
	text, diff, message, diagnostic, stage string
	width                                  int
	dark, stopping                         bool
}

// Modal marks states that own keyboard and mouse above the dashboard.
func (f *Form) Modal() bool {
	return f.reviewModal() || f.DraftPopupActive()
}

func (f *Form) reviewModal() bool {
	return f.initialReview || f.stage == "review" || f.stage == "building" || f.stage == "applying" || f.stage == "result"
}

func (f *Form) modalButtons() [][2]string {
	switch f.stage {
	case "review":
		return [][2]string{{"back", "Back Esc"}, {"apply", "Apply ^S"}}
	case "result":
		return [][2]string{{"return", "Close Enter"}}
	default:
		return [][2]string{{"cancel", "Cancel Esc"}}
	}
}

func (f *Form) modalLayout() reviewLayout {
	width, height := f.canvasSize()
	margin := 0
	if width >= 60 {
		margin = 4
	} else if width >= 16 {
		margin = 1
	}
	w := min(108, max(1, width-margin*2))
	h := min(28, max(1, height-4))
	if height < 10 {
		h = height
	}
	l := reviewLayout{box: rect{(width - w) / 2, (height - h) / 2, w, h}}
	l.body = rect{l.box.x + 2, l.box.y + 2, max(1, w-4), max(1, h-5)}
	if h < 6 || w < 8 {
		l.body = l.box
		return l
	}
	x := l.box.x + 2
	for _, button := range f.modalButtons() {
		bw := ansi.StringWidth(button[1]) + 4
		if x+bw <= l.box.x+l.box.w-1 {
			l.hits = append(l.hits, hitRegion{button[0], rect{x, l.box.y + l.box.h - 2, bw, 1}})
		}
		x += bw + 1
	}
	return l
}

func (f *Form) reviewLines() []string {
	l := f.modalLayout()
	diagnostic := ""
	if f.err != nil {
		diagnostic = f.err.Error()
	}
	key := reviewPresentationKey{f.review.Text, f.review.Diff, f.message, diagnostic, f.stage, max(4, l.body.w), f.dark, f.cancelRequested}
	if f.presentation.lines != nil && f.presentation.key == key {
		return f.presentation.lines
	}
	t := styles(f.dark)
	wrap := func(text string) []string {
		return strings.Split(ansi.Hardwrap(strings.ReplaceAll(safe(text), "\t", "    "), key.width, true), "\n")
	}
	var lines []string
	switch f.stage {
	case "review":
		text, diff := f.review.Text, f.review.Diff
		// Older confirmation callers still pass a linear diff. Keep those
		// readable while new workflows provide the explicit Diff field.
		if diff == "" {
			if start := strings.Index(text, "--- current\n+++ proposed\n"); start >= 0 {
				text, diff = strings.TrimRight(text[:start], "\n"), text[start:]
			}
		}
		if text != "" {
			lines = append(lines, wrap(text)...)
		}
		if diff != "" {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, t.Title.Render("Changes · - before / + after"))
			lines = append(lines, renderReviewDiff(diff, key.width, f.dark)...)
		}
	case "applying":
		label := "Applying to the reviewed target…"
		if f.cancelRequested {
			label = "Stopping; waiting for the dispatched operation's result…"
		}
		lines = append(lines, wrap(label)...)
		lines = append(lines, "")
		lines = append(lines, wrap("Cancellation cannot undo an already dispatched write.")...)
	case "result":
		if f.message != "" {
			heading, details, more := strings.Cut(f.message, "\n")
			style := t.Success.Bold(true)
			if f.err != nil {
				style = t.Error.Bold(true)
			}
			for _, line := range wrap(heading) {
				lines = append(lines, style.Render(line))
			}
			if more {
				lines = append(lines, wrap(details)...)
			}
		}
		if f.err != nil {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			for _, line := range wrap("Error: " + diagnostic) {
				lines = append(lines, t.Error.Render(line))
			}
		}
		if len(lines) == 0 {
			lines = append(lines, "Completed.")
		}
	default:
		lines = wrap("Preparing review…")
	}
	f.presentation = reviewPresentation{key: key, lines: lines}
	return lines
}

func (f *Form) scrollReview(delta int) {
	f.scroll = max(0, min(f.scroll+delta, max(0, len(f.reviewLines())-f.modalLayout().body.h)))
}

func (f *Form) reviewScrollKey(key string) {
	switch key {
	case "down", "j":
		f.scrollReview(1)
	case "up", "k":
		f.scrollReview(-1)
	case "pgdown":
		f.scrollReview(f.modalLayout().body.h)
	case "pgup":
		f.scrollReview(-f.modalLayout().body.h)
	case "home", "g":
		f.scroll = 0
	case "end", "G":
		f.scrollReview(len(f.reviewLines()))
	}
}

func (f *Form) modalView() string {
	l := f.modalLayout()
	width, height := f.canvasSize()
	return overlayText("", f.modalPanel(), l.box.x, l.box.y, width, height)
}

func (f *Form) modalPanel() string {
	l := f.modalLayout()
	t := styles(f.dark)
	label := "Review"
	if f.stage == "result" {
		label = "Result"
		if f.err != nil {
			label = "Operation failed"
		}
	} else if f.stage == "building" || f.stage == "edit" {
		label = "Preparing review"
	} else if f.stage == "applying" {
		label = "Applying"
	}
	title := label + " · " + safe(f.spec.Title)
	if l.box.h < 6 || l.box.w < 8 {
		return fitLines(title+"\nResize to inspect · Esc back", l.box.w, l.box.h)
	}
	body := f.reviewLines()
	start := min(f.scroll, max(0, len(body)-l.body.h))
	end := min(len(body), start+l.body.h)
	subtitle := "Enter does not apply · ↑↓ scroll"
	if f.stage == "result" {
		subtitle = "Result stays here until you close it"
	} else if f.stage != "review" {
		subtitle = "Working on the reviewed target"
	}
	lines := []string{t.Muted.Render(clip(subtitle, l.box.w-4))}
	for _, line := range body[start:end] {
		lines = append(lines, " "+line)
	}
	for len(lines) < l.box.h-4 {
		lines = append(lines, "")
	}
	status := fmt.Sprintf("%d–%d / %d", min(start+1, len(body)), end, len(body))
	if len(body) > l.body.h {
		status += " · PgUp/PgDn scroll"
	}
	lines = append(lines, " "+t.Muted.Render(clip(status, l.box.w-4)))
	buttons := map[string]string{}
	for _, button := range f.modalButtons() {
		buttons[button[0]] = button[1]
	}
	row := " "
	for _, hit := range l.hits {
		if row != " " {
			row += " "
		}
		row += t.Accent.Render("[ " + buttons[hit.id] + " ]")
	}
	lines = append(lines, row)
	return bordered(title, lines, l.box.w, l.box.h, true, f.dark)
}

// overlayText replaces terminal cells, retaining the base content and styles
// outside the opaque panel. ANSI cuts never split escape codes or graphemes.
func overlayText(base, panel string, x, y, width, height int) string {
	lines := strings.Split(fitLines(base, width, height), "\n")
	for row, line := range strings.Split(panel, "\n") {
		if y+row < 0 || y+row >= len(lines) {
			continue
		}
		under := pad(lines[y+row], width)
		w := min(ansi.StringWidth(line), max(0, width-x))
		left := pad(ansi.Cut(under, 0, x), x)
		right := pad(ansi.Cut(under, x+w, width), max(0, width-x-w))
		lines[y+row] = left + pad(ansi.Cut(line, 0, w), w) + right
	}
	return fitLines(strings.Join(lines, "\n"), width, height)
}

func renderReviewDiff(diff string, width int, dark bool) []string {
	t := styles(dark)
	source := strings.Split(strings.TrimSuffix(strings.ReplaceAll(safe(diff), "\t", "    "), "\n"), "\n")
	var out []string
	appendLine := func(line, other string, kind byte) {
		style := t.Muted
		if kind == '-' {
			style = t.Error
		} else if kind == '+' {
			style = t.Success
		}
		body := line
		if len(body) > 0 {
			body = body[1:]
		}
		styled := style.Render(body)
		if other != "" && (kind == '-' || kind == '+') {
			styled = emphasizeDifference(body, other[1:], style)
		}
		for i, row := range strings.Split(ansi.Hardwrap(styled, max(2, width-2), true), "\n") {
			prefix := string(kind) + " "
			if i > 0 {
				prefix = "│ "
			}
			out = append(out, style.Render(prefix)+row)
		}
	}
	for i := 0; i < len(source); {
		line := source[i]
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "@@") || line == "" || len(line) > 0 && line[0] != '-' && line[0] != '+' && line[0] != ' ' {
			for _, row := range strings.Split(ansi.Hardwrap(line, max(4, width), true), "\n") {
				out = append(out, t.Muted.Render(row))
			}
			i++
			continue
		}
		if line[0] == '-' {
			j := i
			for j < len(source) && strings.HasPrefix(source[j], "-") && !strings.HasPrefix(source[j], "--- ") {
				j++
			}
			k := j
			for k < len(source) && strings.HasPrefix(source[k], "+") && !strings.HasPrefix(source[k], "+++ ") {
				k++
			}
			for n := i; n < j; n++ {
				other := ""
				if j+n-i < k {
					other = source[j+n-i]
				}
				appendLine(source[n], other, '-')
			}
			for n := j; n < k; n++ {
				other := ""
				if i+n-j < j {
					other = source[i+n-j]
				}
				appendLine(source[n], other, '+')
			}
			i = k
			continue
		}
		appendLine(line, "", line[0])
		i++
	}
	return out
}

func emphasizeDifference(value, other string, base lipgloss.Style) string {
	left, right := reviewGraphemes(value), reviewGraphemes(other)
	prefix := 0
	for prefix < min(len(left), len(right)) && left[prefix] == right[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < min(len(left), len(right))-prefix && left[len(left)-1-suffix] == right[len(right)-1-suffix] {
		suffix++
	}
	return base.Render(strings.Join(left[:prefix], "")) + base.Bold(true).Underline(true).Render(strings.Join(left[prefix:len(left)-suffix], "")) + base.Render(strings.Join(left[len(left)-suffix:], ""))
}

func reviewGraphemes(value string) []string {
	var result []string
	for value != "" {
		cluster, _ := ansi.FirstGraphemeCluster(value, ansi.GraphemeWidth)
		result = append(result, cluster)
		value = value[len(cluster):]
	}
	return result
}
