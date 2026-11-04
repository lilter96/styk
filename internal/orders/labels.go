package orders

import (
	"context"
	"encoding/hex"
	"net/url"
	"slices"
	"time"
)

const MaxLabelBytes = 8 << 20

func (c Config) LabelRetention() time.Duration {
	if c.PIIRetentionSeconds > 0 && c.PIIRetentionSeconds < 86400 {
		return time.Duration(c.PIIRetentionSeconds) * time.Second
	}
	return 24 * time.Hour
}

func LabelArtifactURL(workID string) string { return "/api/orders/artifacts/" + url.PathEscape(workID) }

func LabelTaskBound(command Command, receipt Receipt) bool {
	if command.Action != ActionPostingLabelCreate || command.PostingAction == nil || receipt.TaskID == "" || !receipt.LabelTasksVerified {
		return false
	}
	return slices.ContainsFunc(receipt.TaskIDs, func(task ActionTask) bool {
		return task.ID == receipt.TaskID && task.Type == command.PostingAction.Parameters.LabelType
	})
}

func LabelTaskComplete(command Command, receipt Receipt, task LabelTask) bool {
	return LabelTaskBound(command, receipt) && task.ID == receipt.TaskID && task.Code == "completed" && task.ErrorCode == "" && task.CountsKnown && task.PostingsCount == 1 && task.PrintedPostingsCount == 1 && len(task.UnprintedPostingNumbers) == 0
}

type LabelArtifact struct {
	Scope                             Scope
	WorkID, TaskID, LabelType, Digest string
	Size                              int
	Ciphertext                        []byte `json:"-"`
	CreatedAt, ExpiresAt              time.Time
}

func (a LabelArtifact) Validate() error {
	digest, err := hex.DecodeString(a.Digest)
	if err != nil || len(digest) != 32 {
		return ErrInvalid
	}
	if a.Scope.Validate() != nil || !validID(a.WorkID) || !validID(a.TaskID) || (a.LabelType != "big_label" && a.LabelType != "small_label") {
		return ErrInvalid
	}
	if a.Size < 1 || a.Size > MaxLabelBytes || len(a.Ciphertext) < 29 || len(a.Ciphertext) > 12<<20 || len(a.Digest) != 64 {
		return ErrInvalid
	}
	if a.CreatedAt.IsZero() || !a.ExpiresAt.After(a.CreatedAt) || a.ExpiresAt.Sub(a.CreatedAt) > 24*time.Hour {
		return ErrInvalid
	}
	return nil
}

type LabelSealer interface {
	SealOrderLabel(Work, ActionTask, []byte, time.Time) (LabelArtifact, error)
}

type LabelArtifactReader interface {
	OrderLabelArtifact(context.Context, string) (LabelArtifact, error)
}

type LabelPDFReader interface {
	DownloadOrderLabel(context.Context, Scope, string) ([]byte, error)
}
