// Package help contains the same offline concepts for the CLI and TUI.
package help

import (
	"embed"
	"strings"
)

//go:embed topics/*.md
var files embed.FS

type Topic struct{ Name, Title, Summary, Body string }

func Topics() []Topic {
	definitions := [][3]string{
		{"cron-fields", "Reading cron fields", "Minutes, hours, dates, weekdays, steps and dialects"},
		{"execution-environment", "The cron execution environment", "Why a command works in your terminal but fails in cron"},
		{"paths-and-scripts", "Paths and script presets", "Working directories, interpreters, arguments and checks"},
		{"uv", "Python projects and uv", "Choose a project, standalone script or existing virtualenv"},
		{"ssh", "SSH hosts and sources", "Use existing aliases, choose your fleet and authenticate"},
	}
	var out []Topic
	for _, d := range definitions {
		b, _ := files.ReadFile("topics/" + d[0] + ".md")
		out = append(out, Topic{d[0], d[1], d[2], strings.TrimSpace(string(b))})
	}
	return out
}
func Find(name string) (Topic, bool) {
	for _, t := range Topics() {
		if t.Name == name {
			return t, true
		}
	}
	return Topic{}, false
}
