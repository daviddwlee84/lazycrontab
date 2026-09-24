package cli

import (
	"fmt"
	"strings"

	"github.com/daviddwlee84/lazycrontab/internal/schedule"
	"github.com/daviddwlee84/lazycrontab/internal/service"
	"github.com/spf13/cobra"
)

var outputPolicies = []string{"inherit", "files", "stderr-only", "discard"}
var enqueueOutputs = []string{"quiet", "inherit"}
var outputPolicyLabels = map[string]string{"inherit": "Inherit (cron / Pueue logs)", "files": "Files (append)", "stderr-only": "Keep stderr only", "discard": "Discard stdout and stderr"}
var enqueueOutputLabels = map[string]string{"quiet": "Quiet (hide submission stdout)", "inherit": "Inherit (show submission output)"}

func validOutputPolicy(value string) bool {
	for _, policy := range outputPolicies {
		if value == policy {
			return true
		}
	}
	return false
}

func validateOutputFlags(cmd *cobra.Command) error {
	f := cmd.Flags()
	policy, _ := f.GetString("output-policy")
	if f.Changed("output-policy") && !validOutputPolicy(policy) {
		return usage("--output-policy must be inherit, files, stderr-only or discard")
	}
	enqueue, _ := f.GetString("enqueue-output")
	if f.Changed("enqueue-output") && enqueue != "quiet" && enqueue != "inherit" {
		return usage("--enqueue-output must be quiet or inherit")
	}
	if f.Changed("output-policy") && policy != "files" {
		for _, key := range []string{"output", "stderr"} {
			value, _ := f.GetString(key)
			if f.Changed(key) && value != "" {
				return usage("--%s requires --output-policy files; remove the conflicting flag", key)
			}
		}
	}
	return nil
}

func validateOutputFlagValues(cmd *cobra.Command, values map[string]string, wizard bool) error {
	if cmd.Flags().Changed("enqueue-output") && values["runner"] != "pueue" {
		return usage("--enqueue-output requires --runner pueue or an existing Pueue job")
	}
	if !wizard && values["output_policy"] == "files" && values["output"] == "" && values["stderr"] == "" {
		return usage("--output-policy files requires --output or --stderr; use inherit to stop redirecting")
	}
	return nil
}

func applyOutputOverrides(values, overrides map[string]string) {
	_, explicitPolicy := overrides["output_policy"]
	_, outputChanged := overrides["output"]
	_, stderrChanged := overrides["stderr"]
	if !explicitPolicy && (outputChanged || stderrChanged) {
		// Legacy path flags remain useful, including clearing the last redirect.
		values["output_policy"] = service.EffectiveOutputPolicy(service.Recipe{Output: values["output"], Stderr: values["stderr"]})
	}
}

func enqueueOnlyOverrides(overrides map[string]string) bool {
	if _, changed := overrides["enqueue_output"]; !changed {
		return false
	}
	for key := range overrides {
		switch key {
		case "enqueue_output", "name", "remark", "schedule", "enabled", "log":
		default:
			return false
		}
	}
	return true
}

func describeOutputRouting(r service.Recipe, environment map[string]string, dialect schedule.Dialect) string {
	policy := service.EffectiveOutputPolicy(r)
	inherited := "cron"
	if dialect == schedule.Supercronic {
		inherited = "Supercronic daemon logs"
	}
	if r.Runner == "pueue" {
		inherited = "Pueue task logs (pueue log or lazypueue)"
	}
	lines := []string{}
	switch policy {
	case "inherit":
		lines = append(lines, "Task output: stdout and stderr inherited by "+inherited)
	case "stderr-only":
		lines = append(lines, "Task output: stdout discarded; stderr inherited by "+inherited)
	case "discard":
		lines = append(lines, "Task output: stdout and stderr discarded")
	case "files":
		if r.Output != "" {
			lines = append(lines, "Task stdout: append to "+r.Output)
		} else {
			lines = append(lines, "Task stdout: inherited by "+inherited)
		}
		if r.Stderr != "" {
			lines = append(lines, "Task stderr: append to "+r.Stderr)
		} else if r.Output != "" {
			lines = append(lines, "Task stderr: append to the stdout file")
		} else {
			lines = append(lines, "Task stderr: inherited by "+inherited)
		}
		lines = append(lines, "Append files use your explicit paths; lazycrontab does not rotate them.")
	}
	if r.Runner == "pueue" {
		if service.EffectiveEnqueueOutput(r) == "quiet" {
			lines = append(lines, "Enqueue notices: submission stdout suppressed; stderr and exit status retained.")
		} else {
			lines = append(lines, "Enqueue notices: submission stdout/stderr inherited by the scheduler, including Pueue task IDs.")
		}
		lines = append(lines, "Pueue task output is separate from submission output; task failures do not directly become cron mail.")
	}
	if r.Log != "" {
		lines = append(lines, "Inspect log: "+r.Log+" (inspection only; no output redirection)")
	}
	if dialect == schedule.Supercronic {
		lines = append(lines, "Supercronic does not provide cron mail; MAILTO is not a mail-delivery setting here.")
	} else if mailto, set := environment["MAILTO"]; set {
		if mailto == "" {
			lines = append(lines, "Crontab MAILTO is empty: cron mail is disabled for this job.")
		} else {
			lines = append(lines, fmt.Sprintf("Crontab MAILTO=%q: scheduler-visible output may be mailed if local mail delivery is configured.", mailto))
		}
	} else {
		lines = append(lines, "Crontab MAILTO is unset: native cron may mail scheduler-visible output to the crontab owner if local mail delivery is configured.")
	}
	if _, set := r.Environment["MAILTO"]; set {
		lines = append(lines, "Per-job MAILTO in Variables is passed to the command; it does not configure cron's mail delivery.")
	}
	return strings.Join(lines, "\n")
}
