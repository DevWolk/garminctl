package cli

import (
	"context"
	"errors"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/clix"
)

func (c *Command) snapshots(ctx context.Context, args []string) error {
	if c.Snapshots == nil {
		return errors.New("application recovery snapshots are not configured")
	}

	action, err := c.Snapshots()
	if err != nil {
		return err
	}

	if action == nil {
		return errors.New("snapshot recovery returned no action")
	}

	if args[0] == snapshotsCommand {
		return c.listSnapshots(ctx, action)
	}

	if args[0] == removeSnapshotCommand {
		if err := action.Remove(ctx, args[1]); err != nil {
			return err
		}

		return c.removed("data snapshot", args[1])
	}

	value, err := action.Verify(ctx, args[1])
	if err != nil {
		return err
	}

	files := make([]snapshotFileInfo, 0, len(value.Files))
	for _, file := range value.Files {
		files = append(files, snapshotFileInfo{Area: file.Area, Path: file.Path, Bytes: file.Bytes, SHA256: file.Digest})
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, struct {
			Verified          bool               `json:"verified"`
			Verification      string             `json:"verification"`
			DataCompatibility string             `json:"dataCompatibility"`
			Snapshot          snapshotSummary    `json:"snapshot"`
			Files             []snapshotFileInfo `json:"files"`
			MissingSources    []string           `json:"missingSources"`
		}{Verified: true, Verification: "local_manifest", DataCompatibility: "not_checked", Snapshot: summarizeSnapshot(*value), Files: files, MissingSources: append([]string{}, value.MissingSources...)})
	}

	c.Out.Printf("Snapshot %s before %s -> %s\n", value.ID, value.Current.Tag, value.Update.Tag)

	for _, file := range files {
		c.Out.Printf("%s/%q  %d bytes  SHA256 %s\n", file.Area, file.Path, file.Bytes, file.SHA256)
	}

	c.Out.Println("Files match the local backup manifest. No data was restored; data compatibility is not checked.")

	return c.Out.Err()
}

type snapshotSummary struct {
	SnapshotID string    `json:"snapshotId"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	CreatedAt  time.Time `json:"createdAt"`
	Files      int       `json:"files"`
	Bytes      int64     `json:"bytes"`
}

type snapshotFileInfo struct {
	Area   string `json:"area"`
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func summarizeSnapshot(value application.RecoverySnapshot) snapshotSummary {
	result := snapshotSummary{SnapshotID: value.ID, From: value.Current.Tag, To: value.Update.Tag, CreatedAt: value.CreatedAt, Files: len(value.Files)}
	for _, file := range value.Files {
		result.Bytes += file.Bytes
	}

	return result
}

func (c *Command) listSnapshots(ctx context.Context, action *application.RecoverySnapshotsAction) error {
	rows, unusable, err := action.Catalog(ctx)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, snapshotListJSON(rows, unusable))
	}

	c.Out.Println("Snapshot metadata only; inspect an ID to verify its retained files.")

	for _, row := range rows {
		value := summarizeSnapshot(row)
		c.Out.Printf("%s  before %s -> %s  %d files  %d bytes\n", value.SnapshotID, value.From, value.To, value.Files, value.Bytes)
	}

	c.warnUnusable("data snapshot", removeSnapshotCommand, unusable)

	return c.Out.Err()
}

type snapshotListResult struct {
	Verified  bool              `json:"verified"`
	Snapshots []snapshotSummary `json:"snapshots"`
	Unusable  []unusableInfo    `json:"unusable"`
}

func snapshotListJSON(rows []application.RecoverySnapshot, unusable []application.UnusableEntry) snapshotListResult {
	result := snapshotListResult{Snapshots: make([]snapshotSummary, 0, len(rows)), Unusable: unusableList(unusable)}
	for _, row := range rows {
		result.Snapshots = append(result.Snapshots, summarizeSnapshot(row))
	}

	return result
}
