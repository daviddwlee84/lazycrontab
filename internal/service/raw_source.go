package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
)

// MaxRawSourceBytes matches the transport's bounded source read. Editor input
// is bounded before planning so an oversized file never reaches source writes.
const MaxRawSourceBytes = 16 << 20

// PlanRawSource reviews the exact document bytes without normalizing entries,
// allocating job IDs, changing sidecars, or writing anything to the target.
func (s *Service) PlanRawSource(ctx context.Context, snap Snapshot, after string) (Plan, error) {
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	if snap.Document == nil {
		return Plan{}, fmt.Errorf("no source snapshot")
	}
	_, src, err := s.source(snap.Host, snap.Source)
	if err != nil {
		return Plan{}, err
	}
	if src.ReadOnly || src.Kind == "system" || snap.Document.SystemFile {
		return Plan{}, fmt.Errorf("source is read-only")
	}
	if len(after) > MaxRawSourceBytes {
		return Plan{}, fmt.Errorf("crontab exceeds the %d-byte source limit", MaxRawSourceBytes)
	}
	if offset := strings.IndexByte(after, 0); offset >= 0 {
		return Plan{}, fmt.Errorf("line %d: crontab cannot contain NUL bytes", strings.Count(after[:offset], "\n")+1)
	}
	if snap.Document.Dialect == schedule.System && after != "" && !strings.HasSuffix(after, "\n") {
		return Plan{}, fmt.Errorf("line %d: native crontab requires a final newline; add a newline at the end of the file", strings.Count(after, "\n")+1)
	}
	issues, err := document.ValidateRawEdit(snap.Document.Raw, after, snap.Document.Dialect)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Host: snap.Host, Source: snap.Source, Operation: "edit-source", Before: snap.Document.Raw, After: after,
		Revision: snap.Document.Revision, Existed: snap.Exists, Diff: Diff(snap.Document.Raw, after),
		Warnings: []string{
			"This edit replaces the entire selected source, including comments and environment assignments.",
			"Changing a command or lazycrontab ID can make its helper recipe unavailable. Helper metadata and managed script files are not rewritten or deleted by raw source editing.",
		},
	}
	for _, issue := range issues {
		p.Warnings = append(p.Warnings, issue.String())
	}
	if snap.Document.Dialect == schedule.Supercronic {
		p.Warnings = append(p.Warnings, "Saving the file does not confirm that a running Supercronic instance reloaded it.")
	}
	return p, nil
}
