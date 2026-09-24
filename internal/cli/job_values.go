package cli

import (
	"maps"

	"github.com/daviddwlee84/lazycrontab/internal/service"
)

// effectiveJobValues projects a retained wizard draft into one execution type.
// Hidden values stay in the original map so switching back restores the draft;
// they do not participate in validation, compilation or change detection.
// commandScript is a baseline/explicit CLI metadata association, not an inactive
// script payload that happened to be edited while visiting another preset.
func effectiveJobValues(draft map[string]string, commandScript string) map[string]string {
	v := maps.Clone(draft)
	if v == nil {
		v = map[string]string{}
	}
	if v["preset"] == "" {
		v["preset"] = "command"
	}
	if v["runner"] == "" {
		v["runner"] = "direct"
	}
	clear := func(keys ...string) {
		for _, key := range keys {
			v[key] = ""
		}
	}
	switch v["preset"] {
	case "command":
		v["script"] = commandScript
		clear("script_content", "runtime", "project", "args")
	case "executable":
		clear("command", "script_content", "runtime", "project")
	case "shell", "python", "uv-script":
		clear("command", "script_content", "project")
	case "uv-project":
		clear("command", "script_content")
	case "managed-shell":
		clear("command", "script", "project")
		if v["runtime"] == "" {
			v["runtime"] = "/bin/sh"
		}
	}
	if v["runner"] != "pueue" {
		v["group"] = ""
		v["enqueue_output"] = ""
	}
	if v["output_policy"] == "" {
		v["output_policy"] = service.EffectiveOutputPolicy(service.Recipe{Output: v["output"], Stderr: v["stderr"]})
	}
	if v["output_policy"] != "files" {
		clear("output", "stderr")
	}
	return v
}

func sameExecutionValues(a, b map[string]string) bool {
	for _, key := range []string{"command", "runner", "group", "directory", "output_policy", "enqueue_output", "output", "stderr", "script", "preset", "runtime", "project", "args", "environment", "script_content"} {
		if a[key] != b[key] {
			return false
		}
	}
	return true
}

func sameExecutionExceptEnqueue(a, b map[string]string) bool {
	a = maps.Clone(a)
	a["enqueue_output"] = b["enqueue_output"]
	return sameExecutionValues(a, b)
}

func pueueGroupDisabled(values map[string]string) string {
	if values["runner"] != "pueue" {
		return "Select Pueue to choose a queue group."
	}
	return ""
}

func pueueEnqueueDisabled(values map[string]string) string {
	if values["runner"] != "pueue" {
		return "Select Pueue to configure its submission notices."
	}
	return ""
}

func outputPathDisabled(values map[string]string) string {
	if values["output_policy"] != "files" {
		return "Choose Files in Task output to append to explicit paths."
	}
	return ""
}
