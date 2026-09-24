package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/daviddwlee84/lazycrontab/internal/service"
)

func TestChangeReviewAndHumanReceiptKeepJSONContract(t *testing.T) {
	_, cron := isolated(t)
	raw := "# lazycrontab: {\"v\":1,\"id\":\"example\",\"name\":\"Hourly greeting\"}\n0 * * * * echo hi\n"
	if err := os.WriteFile(cron, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := invoke("disable", "example", "--dry-run")
	if err != nil || !strings.Contains(preview, "Job: Hourly greeting") || !strings.Contains(preview, "Status: Enabled → Disabled") || !strings.Contains(preview, "--- current") || !strings.Contains(preview, "+# lazycrontab-disabled:") {
		t.Fatal(preview, err)
	}
	if unchanged, err := os.ReadFile(cron); err != nil || string(unchanged) != raw {
		t.Fatal("review changed source", err)
	}
	out, err := invoke("disable", "example", "--yes")
	for _, want := range []string{"Saved · Job disabled", "Target: local/user", "Job ID: example", "Backup:"} {
		if err != nil || !strings.Contains(out, want) {
			t.Fatal(out, err)
		}
	}
	if json.Valid([]byte(out)) || strings.Contains(out, `"revision"`) {
		t.Fatal("human result is JSON", out)
	}
	out, err = invoke("enable", "example", "--yes", "--json")
	var receipt service.Receipt
	if err != nil || json.Unmarshal([]byte(out), &receipt) != nil || receipt.Status != "saved" || receipt.Revision == "" || receipt.Backup == "" {
		t.Fatal("machine receipt changed", out, err)
	}
}

func TestHumanReceiptDoesNotHidePartialOrUnknownOutcomes(t *testing.T) {
	for _, status := range []string{"failed", "unknown", "unchanged", "saved"} {
		r := service.Receipt{Host: "lab", Source: "user", Status: status, ScriptPath: "/data/script.sh", ScriptStatus: "saved", Message: "The script was retained."}
		out := receiptText(r, "edit", "example")
		if strings.HasPrefix(out, "Saved") != (status == "saved") {
			t.Fatalf("%s: %s", status, out)
		}
		for _, want := range []string{"Target: lab/user", "/data/script.sh", "Script status: saved", r.Message} {
			if !strings.Contains(out, want) {
				t.Fatalf("missing partial outcome %q: %s", want, out)
			}
		}
		if status == "unknown" && !strings.Contains(out, "Refresh the source before retrying") {
			t.Fatal(out)
		}
	}
}

func TestQueuedRunResultReportsSubmissionNotExecution(t *testing.T) {
	out := runRecordText(service.RunRecord{Host: "lab", Source: "user", JobID: "example", Status: "queued", TaskID: "140", Output: "140\n"})
	if !strings.Contains(out, "Queued in Pueue · task 140") || strings.Contains(out, "Completed") || strings.Contains(out, "\nOutput\n") {
		t.Fatal(out)
	}
	plan := runPlanText(service.ExecutionPlan{Runner: "pueue", Directory: "/home/test", Shell: "/bin/sh", Command: "pueue add --working-directory /project -- 'echo hi'"})
	if !strings.Contains(plan, "Submission working directory: /home/test") || !strings.Contains(plan, "Submission shell: /bin/sh") || !strings.Contains(plan, "--working-directory /project") {
		t.Fatal("submission context was presented as task settings", plan)
	}
}

func TestJobJSONResultRetainsFullReceipt(t *testing.T) {
	r := service.Receipt{Host: "lab", Source: "user", Status: "saved", Backup: "/state/backups/snapshot.json", Revision: "source-revision"}
	data := jobReview{Plan: service.Plan{JobID: "example"}}
	result := jobJSONResult(data, r, nil)
	var decoded service.Receipt
	encoded := strings.TrimSuffix(result["result"].(string), "\nJob ID: example")
	if result["job_id"] != "example" || json.Unmarshal([]byte(encoded), &decoded) != nil || decoded != r {
		t.Fatal("add/edit JSON dropped machine receipt fields", result)
	}
}
