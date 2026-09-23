package ui

import (
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type uiStyles struct {
	Title, Accent, Muted, Success, Warning, Error, Schedule, Selected, Border lipgloss.Style
}

func styles(dark bool) uiStyles {
	s := uiStyles{}
	if os.Getenv("NO_COLOR") != "" {
		s.Title = lipgloss.NewStyle().Bold(true)
		s.Selected = lipgloss.NewStyle().Bold(true).Underline(true)
		return s
	}
	colors := []string{"#0969da", "#57606a", "#1a7f37", "#9a6700", "#cf222e", "#8250df", "#ddf4ff", "#0969da", "#8c959f"}
	if dark {
		colors = []string{"#89dceb", "#9399b2", "#a6e3a1", "#f9e2af", "#f38ba8", "#cba6f7", "#313f58", "#cdd6f4", "#585b70"}
	}
	fg := func(i int) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(colors[i])) }
	s.Title = fg(0).Bold(true)
	s.Accent = fg(0)
	s.Muted = fg(1)
	s.Success = fg(2)
	s.Warning = fg(3)
	s.Error = fg(4)
	s.Schedule = fg(5)
	s.Border = fg(8)
	s.Selected = lipgloss.NewStyle().Background(lipgloss.Color(colors[6])).Foreground(lipgloss.Color(colors[7])).Bold(true)
	return s
}

type rect struct{ x, y, w, h int }

func (r rect) contains(x, y int) bool {
	return r.w > 0 && r.h > 0 && x >= r.x && y >= r.y && x < r.x+r.w && y < r.y+r.h
}

type hitRegion struct {
	id   string
	rect rect
}

func hitAt(hits []hitRegion, x, y int) string {
	for _, h := range hits {
		if h.rect.contains(x, y) {
			return h.id
		}
	}
	return ""
}

func fitLines(content string, width, height int) string {
	if width < 1 || height < 1 {
		return ""
	}
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = clip(lines[i], width)
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
func pad(s string, w int) string {
	s = clip(s, w)
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

func bordered(title string, lines []string, width, height int, focused bool, dark bool) string {
	if width < 4 || height < 3 {
		return fitLines(strings.Join(lines, "\n"), width, height)
	}
	t := styles(dark)
	edge := t.Border
	if focused {
		edge = t.Accent
	}
	label := " " + clip(title, max(1, width-4)) + " "
	top := edge.Render("╭" + label + strings.Repeat("─", max(0, width-2-ansi.StringWidth(label))) + "╮")
	out := []string{top}
	for i := 0; i < height-2; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		out = append(out, edge.Render("│")+pad(line, width-2)+edge.Render("│"))
	}
	out = append(out, edge.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	return strings.Join(out, "\n")
}
