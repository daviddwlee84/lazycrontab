package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/daviddwlee84/lazycrontab/internal/service"
	"github.com/daviddwlee84/lazycrontab/internal/transport"
	"github.com/daviddwlee84/lazycrontab/internal/ui"
	"github.com/spf13/cobra"
)

type rawSourceView struct {
	Host     string `json:"host"`
	Source   string `json:"source"`
	Kind     string `json:"kind"`
	Path     string `json:"path,omitempty"`
	Revision string `json:"revision"`
	Content  string `json:"content"`
	ReadOnly bool   `json:"read_only"`
	Exists   bool   `json:"exists"`
}

func addRawSourceCommands(group *cobra.Command, o *options) {
	group.AddCommand(&cobra.Command{Use: "show", Short: "Print the exact raw crontab for one host/source", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		s, snap, err := o.snapshot(cmd)
		if err != nil {
			return err
		}
		src, err := s.Config.Source(snap.Host, snap.Source)
		if err != nil {
			return err
		}
		if o.json {
			return o.emit(cmd, rawSourceView{Host: snap.Host, Source: snap.Source, Kind: src.Kind, Path: src.Path, Revision: snap.Document.Revision, Content: snap.Document.Raw, ReadOnly: src.ReadOnly || src.Kind == "system", Exists: snap.Exists}, "")
		}
		_, err = io.WriteString(cmd.OutOrStdout(), snap.Document.Raw)
		return err
	}})
	var inputFile string
	edit := &cobra.Command{Use: "edit-raw", Short: "Edit a private raw-crontab copy, then review and install", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		fromFile := cmd.Flags().Changed("file")
		if fromFile && inputFile == "" {
			return usage("--file requires a local file path")
		}
		if !fromFile && o.dry {
			return usage("sources edit-raw --dry-run requires --file LOCAL_FILE; no editor is opened during a dry run")
		}
		if !fromFile && (o.json || !tty()) {
			return usage("sources edit-raw requires a terminal; use --file LOCAL_FILE with --dry-run or --yes")
		}
		s, snap, err := o.snapshot(cmd)
		if err != nil {
			return err
		}
		if err := writableRawSource(s, snap); err != nil {
			return err
		}
		if !fromFile {
			return o.editRawSource(cmd, s, snap)
		}
		after, err := readRawSourceFile(inputFile)
		if err != nil {
			return err
		}
		return o.reviewRawSource(cmd, s, snap, after)
	}}
	edit.Flags().StringVar(&inputFile, "file", "", "Local file containing the complete replacement crontab")
	group.AddCommand(edit)
}

func writableRawSource(s *service.Service, snap service.Snapshot) error {
	if snap.Document == nil {
		return fmt.Errorf("no source snapshot")
	}
	src, err := s.Config.Source(snap.Host, snap.Source)
	if err != nil {
		return err
	}
	if src.ReadOnly || src.Kind == "system" {
		return fmt.Errorf("source %s/%s is read-only", snap.Host, snap.Source)
	}
	return nil
}

func (o *options) editRawSource(cmd *cobra.Command, s *service.Service, snap service.Snapshot) error {
	return o.editRawSourceWithRetry(cmd, s, snap, ui.RetrySourceEdit)
}

type rawSourceRetry func(context.Context, string, string, bool, string) (bool, error)

func (o *options) editRawSourceWithRetry(cmd *cobra.Command, s *service.Service, snap service.Snapshot, retry rawSourceRetry) error {
	if err := writableRawSource(s, snap); err != nil {
		return err
	}
	if len(snap.Document.Raw) > service.MaxRawSourceBytes {
		return fmt.Errorf("source exceeds the %d-byte raw-edit limit", service.MaxRawSourceBytes)
	}
	file, err := os.CreateTemp("", "lazycrontab-source-*.cron")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		_, err = io.WriteString(file, snap.Document.Raw)
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	for {
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		invocation := editor(file.Name())
		editorCmd := exec.CommandContext(cmd.Context(), invocation.Path, invocation.Args[1:]...)
		if err = transport.Interactive(editorCmd); err != nil {
			return fmt.Errorf("editor exited without saving the source: %w", err)
		}
		after, validationErr := readRawSourceFile(file.Name())
		if validationErr == nil && after == snap.Document.Raw {
			return o.reviewRawSource(cmd, s, snap, after)
		}
		var plan service.Plan
		if validationErr == nil {
			plan, validationErr = s.PlanRawSource(cmd.Context(), snap, after)
		}
		if validationErr == nil {
			// Only validation retries. Keep the original revision across edits;
			// a failed or unknown Apply must never repeat an external write.
			return o.applyPlan(cmd, s, plan)
		}
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		again, err := retry(cmd.Context(), "Repair raw source · "+snap.Host+"/"+snap.Source, validationErr.Error(), s.Config.Mouse, s.Config.Theme)
		if err != nil {
			return err
		}
		if !again {
			return ui.ErrCancelled
		}
	}
}

func (o *options) reviewRawSource(cmd *cobra.Command, s *service.Service, snap service.Snapshot, after string) error {
	if after == snap.Document.Raw && !o.dry {
		return o.emit(cmd, service.Receipt{Host: snap.Host, Source: snap.Source, Status: "unchanged", Revision: snap.Document.Revision}, "No changes to "+snap.Host+"/"+snap.Source)
	}
	plan, err := s.PlanRawSource(cmd.Context(), snap, after)
	if err != nil {
		return err
	}
	return o.applyPlan(cmd, s, plan)
}

func readRawSourceFile(path string) (string, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("inspect local crontab file: %w", err)
	}
	if !stat.Mode().IsRegular() {
		return "", usage("crontab input must be a regular local file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read local crontab file: %w", err)
	}
	defer file.Close()
	stat, err = file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect local crontab file: %w", err)
	}
	if !stat.Mode().IsRegular() {
		return "", usage("crontab input must be a regular local file")
	}
	if stat.Size() > service.MaxRawSourceBytes {
		return "", usage("raw crontab exceeds %d bytes", service.MaxRawSourceBytes)
	}
	content, err := io.ReadAll(io.LimitReader(file, service.MaxRawSourceBytes+1))
	if err != nil {
		return "", fmt.Errorf("read local crontab file: %w", err)
	}
	if len(content) > service.MaxRawSourceBytes {
		return "", usage("raw crontab exceeds %d bytes", service.MaxRawSourceBytes)
	}
	return string(content), nil
}
