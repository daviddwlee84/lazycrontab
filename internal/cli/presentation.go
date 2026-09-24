package cli

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/service"
	"github.com/daviddwlee84/lazycrontab/internal/ui"
)

// Retain the existing add/edit JSON result independently of the TUI summary.
func jobJSONResult(data jobReview, receipt service.Receipt, err error) map[string]any {
	result := pretty(receipt)
	if err == nil {
		result += "\nJob ID: " + data.Plan.JobID
	}
	return map[string]any{"job_id": data.Plan.JobID, "result": result}
}

// Presentation stays separate from the machine-readable plan and receipt.
// In particular, an uncertain write must never look like a successful save.
func receiptText(r service.Receipt, operation, jobID string) string {
	heading := "Not saved"
	switch r.Status {
	case "saved":
		action := map[string]string{"add": "Job added", "edit": "Job updated", "enable": "Job enabled", "disable": "Job disabled", "remove": "Job removed", "script": "Script saved", "restore": "Backup restored"}[operation]
		if action == "" {
			action = "Crontab updated"
		}
		heading = "Saved · " + action
	case "unchanged":
		heading = "No changes"
	case "unknown":
		heading = "Save outcome unknown"
	}
	lines := []string{heading, "Target: " + r.Host + "/" + r.Source}
	if jobID != "" {
		lines = append(lines, "Job ID: "+jobID)
	}
	if r.ScriptPath != "" {
		lines = append(lines, "Script: "+r.ScriptPath)
		if r.ScriptStatus != "" {
			lines = append(lines, "Script status: "+r.ScriptStatus)
		}
	}
	if r.Backup != "" {
		lines = append(lines, "Backup: "+strings.TrimSuffix(filepath.Base(r.Backup), ".json"))
	}
	if r.Message != "" {
		lines = append(lines, "", r.Message)
	}
	if r.Status == "unknown" {
		lines = append(lines, "", "Refresh the source before retrying; the write may already have reached the target.")
	}
	return strings.Join(lines, "\n")
}

func changeReview(p service.Plan, j document.Job) ui.Review {
	name := j.Name
	if name == "" {
		name = j.ID
	}
	lines := []string{"Job: " + name, "Target: " + p.Host + "/" + p.Source, "Job ID: " + j.ID}
	state := "Enabled"
	if !j.Enabled {
		state = "Disabled"
	}
	switch p.Operation {
	case "enable":
		lines = append(lines, "Status: "+state+" → Enabled", "Future scheduled runs will be enabled.")
	case "disable":
		lines = append(lines, "Status: "+state+" → Disabled", "Future scheduled runs will be skipped.")
	case "remove":
		lines = append(lines, "Remove this job from the crontab.")
	}
	lines = append(lines, "Schedule: "+j.Schedule)
	e := service.Entry{Host: p.Host, Source: p.Source, Job: j}
	if recipe, err := service.LoadRecipe(e); err == nil && recipe.Runner == "pueue" {
		lines = append(lines, "Existing Pueue tasks are unaffected.")
	}
	if len(p.Warnings) > 0 {
		lines = append(lines, "", strings.Join(p.Warnings, "\n"))
	}
	return ui.Review{Text: strings.Join(lines, "\n"), Diff: p.Diff, Data: p}
}

func runPlanText(p service.ExecutionPlan) string {
	runner := "Direct"
	directoryLabel, shellLabel := "Working directory", "Shell"
	if p.Runner == "pueue" {
		runner = "Pueue (enqueue)"
		directoryLabel, shellLabel = "Submission working directory", "Submission shell"
	}
	lines := []string{"Target: " + p.Host + "/" + p.Source, "Job ID: " + p.JobID, "Runner: " + runner, directoryLabel + ": " + p.Directory, shellLabel + ": " + p.Shell}
	if p.Runner == "pueue" {
		lines = append(lines, "The queued task uses the shell and working directory configured by Pueue and the command below.")
	}
	lines = append(lines, "", "Command", p.Command)
	if len(p.Environment) > 0 {
		lines = append(lines, "", "Environment")
		for _, key := range slices.Sorted(maps.Keys(p.Environment)) {
			lines = append(lines, key+"="+p.Environment[key])
		}
	}
	if p.InheritEnvironment {
		lines = append(lines, "Environment also inherits from the selected host process.")
	}
	if p.Stdin != "" {
		lines = append(lines, "", "Standard input", p.Stdin)
	}
	if p.Warning != "" {
		lines = append(lines, "", p.Warning)
	}
	return strings.Join(lines, "\n")
}

func runRecordText(r service.RunRecord) string {
	heading := "Run failed"
	switch r.Status {
	case "completed":
		heading = fmt.Sprintf("Completed · exit %d", r.ExitCode)
	case "queued":
		heading = "Queued in Pueue · task " + r.TaskID
	case "unknown":
		heading = "Run outcome unknown · inspect the target before retrying"
	}
	lines := []string{heading, "Target: " + r.Host + "/" + r.Source, "Job ID: " + r.JobID}
	if !r.Finished.IsZero() {
		lines = append(lines, "Finished: "+r.Finished.Format("Mon 2006-01-02 15:04:05 -07:00"))
	}
	if r.Status == "queued" {
		lines = append(lines, "Pueue captures stdout/stderr. Inspect with pueue log or lazypueue.")
	} else if r.Output != "" {
		lines = append(lines, "", "Output", r.Output)
	}
	if r.Stderr != "" {
		lines = append(lines, "", "Error output", r.Stderr)
	}
	if r.OutputTruncated {
		lines = append(lines, "Output was truncated.")
	}
	if r.Error != "" {
		lines = append(lines, "", r.Error)
	}
	return strings.Join(lines, "\n")
}
