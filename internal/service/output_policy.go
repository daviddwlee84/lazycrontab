package service

import (
	"fmt"
	"strings"

	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
)

const (
	OutputInherit    = "inherit"
	OutputFiles      = "files"
	OutputStderrOnly = "stderr-only"
	OutputDiscard    = "discard"
	EnqueueQuiet     = "quiet"
	EnqueueInherit   = "inherit"
)

// PueueSubmission freezes the already compiled enqueue invocation, including
// native cron percent encoding. Manual Run may expose its task ID only after
// verifying that it renders back to the current, digest-bound source command.
// Neither Run nor an output-only migration rebuilds this command from a recipe.
type PueueSubmission struct {
	Version int              `json:"v"`
	Dialect schedule.Dialect `json:"dialect"`
	Command string           `json:"command"`
}

// EffectiveOutputPolicy retains the meaning of recipes written before policies
// existed, including stderr-only file redirects with inherited stdout.
func EffectiveOutputPolicy(r Recipe) string {
	if r.OutputPolicy != "" {
		return r.OutputPolicy
	}
	if r.Output != "" || r.Stderr != "" {
		return OutputFiles
	}
	return OutputInherit
}

// EffectiveEnqueueOutput deliberately preserves existing scheduled output when
// old metadata has no policy. Defaults for newly created jobs belong to the UI.
func EffectiveEnqueueOutput(r Recipe) string {
	if r.EnqueueOutput != "" {
		return r.EnqueueOutput
	}
	return EnqueueInherit
}

func ValidOutputPolicy(value string) bool {
	switch value {
	case OutputInherit, OutputFiles, OutputStderrOnly, OutputDiscard:
		return true
	}
	return false
}

func ValidEnqueueOutput(value string) bool {
	return value == EnqueueQuiet || value == EnqueueInherit
}

func validateOutputPolicies(r Recipe) error {
	policy := EffectiveOutputPolicy(r)
	if !ValidOutputPolicy(policy) {
		return fmt.Errorf("output policy must be inherit, files, stderr-only or discard")
	}
	if policy == OutputFiles && r.Output == "" && r.Stderr == "" {
		return fmt.Errorf("files output policy requires an output or stderr path")
	}
	if !ValidEnqueueOutput(EffectiveEnqueueOutput(r)) {
		return fmt.Errorf("enqueue output must be quiet or inherit")
	}
	return nil
}

// Compile keeps its existing scheduled-command interface. Callers that persist
// helper metadata should use CompileRecipe to retain the frozen submission.
func Compile(e Entry, r Recipe) (string, error) {
	command, _, err := CompileRecipe(e, r)
	return command, err
}

// CompileRecipe builds a scheduled command and the matching helper metadata.
// Task output is handled inside its payload; enqueue output is handled outside
// it, so discarding task output never discards Pueue client errors.
func CompileRecipe(e Entry, r Recipe) (string, Recipe, error) {
	command, err := compileTask(e, r)
	if err != nil {
		return "", r, err
	}
	r.PueueSubmission = nil
	if r.Runner == "pueue" {
		r.PueueSubmission = &PueueSubmission{Version: 1, Dialect: e.Dialect, Command: command}
		command = renderScheduledSubmission(command, EffectiveEnqueueOutput(r))
	}
	r.CommandDigest = document.Digest(command)
	return command, r, nil
}

// CompileManual is for explicit Run overrides. Default Run instead consumes a
// verified frozen submission, preserving pinned shell/executable choices.
func CompileManual(e Entry, r Recipe) (string, error) {
	return compileTask(e, r)
}

func renderScheduledSubmission(command, policy string) string {
	if policy == EnqueueQuiet {
		return command + " > /dev/null"
	}
	return command
}

func verifySubmissionBinding(e Entry, r Recipe) error {
	if r.CommandDigest == "" || r.CommandDigest != document.Digest(e.Command) {
		return fmt.Errorf("Pueue submission metadata does not match the current command; explicitly rebuild and review the recipe")
	}
	return nil
}

func frozenSubmission(e Entry, r Recipe) (string, error) {
	if err := verifySubmissionBinding(e, r); err != nil {
		return "", err
	}
	frozen := r.PueueSubmission
	if frozen == nil || frozen.Version != 1 || frozen.Dialect != e.Dialect || strings.TrimSpace(frozen.Command) == "" || strings.ContainsRune(frozen.Command, '\x00') {
		return "", fmt.Errorf("Pueue submission metadata is missing or unsupported; explicitly rebuild and review the recipe")
	}
	policy := EffectiveEnqueueOutput(r)
	if !ValidEnqueueOutput(policy) || renderScheduledSubmission(frozen.Command, policy) != e.Command {
		return "", fmt.Errorf("frozen Pueue submission does not reproduce the current command; explicitly rebuild and review the recipe")
	}
	return frozen.Command, nil
}

func storedManualSubmission(e Entry, r Recipe) (string, error) {
	if r.PueueSubmission != nil || EffectiveEnqueueOutput(r) != EnqueueInherit {
		return frozenSubmission(e, r)
	}
	// Legacy Run remains byte-for-byte authoritative. There is no owned quiet
	// wrapper to bypass, and arbitrary user redirects must never be removed.
	return e.Command, nil
}

// UpdateEnqueueOutput changes only the client stdout policy of a saved Pueue
// job. It neither probes a daemon nor recompiles task paths, shell or payload.
// The caller must still review/apply the returned command and save its recipe.
func UpdateEnqueueOutput(e Entry, r Recipe, policy string) (string, Recipe, error) {
	if r.Runner != "pueue" {
		return "", r, fmt.Errorf("enqueue output applies only to Pueue jobs")
	}
	if !ValidEnqueueOutput(policy) {
		return "", r, fmt.Errorf("enqueue output must be quiet or inherit")
	}
	if err := verifySubmissionBinding(e, r); err != nil {
		return "", r, err
	}
	command := e.Command
	if r.PueueSubmission != nil || EffectiveEnqueueOutput(r) != EnqueueInherit {
		var err error
		command, err = frozenSubmission(e, r)
		if err != nil {
			return "", r, err
		}
	} else {
		// Previous releases generated this exact argv prefix. Unrecognized raw
		// commands require an explicit rebuild, not inferred redirect surgery.
		prefix := commandJoin([]string{r.PueuePath, "add", "--print-task-id", "--label", "lazycrontab:" + e.ID}) + " "
		if e.Dialect == schedule.System {
			prefix = cronEscape(prefix)
		}
		if r.PueuePath == "" || !strings.HasPrefix(command, prefix) {
			return "", r, fmt.Errorf("legacy Pueue command is not a recognized generated submission; explicitly rebuild and review the recipe")
		}
		if e.Dialect == schedule.System {
			if _, input := SplitPercent(command); input != "" {
				return "", r, fmt.Errorf("legacy Pueue submission uses native cron stdin; explicitly rebuild and review the recipe")
			}
		}
	}
	if policy == EffectiveEnqueueOutput(r) {
		return e.Command, r, nil
	}
	r.EnqueueOutput = policy
	r.PueueSubmission = &PueueSubmission{Version: 1, Dialect: e.Dialect, Command: command}
	command = renderScheduledSubmission(command, policy)
	r.CommandDigest = document.Digest(command)
	return command, r, nil
}
