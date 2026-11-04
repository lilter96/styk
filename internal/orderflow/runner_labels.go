package orderflow

import (
	"context"
	"errors"

	"github.com/lilter96/styk/internal/orders"
)

func (r *Runner) pollLabelTask(ctx context.Context, work orders.Work, taskID string) error {
	if taskID != work.Receipt.TaskID || !orders.LabelTaskBound(work.Command, work.Receipt) {
		return r.deferReadback(ctx, work, "LABEL_TASK_BINDING_UNVERIFIED")
	}
	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "ozon", work.Credentials.Ozon)
	if err != nil {
		return r.deferReadback(ctx, work, "SCOPED_SOURCE_CREDENTIALS_UNAVAILABLE")
	}
	reader, ok := client.(orders.LabelTaskReader)
	if !ok {
		return r.deferReadback(ctx, work, "LABEL_TASK_READER_UNAVAILABLE")
	}
	task, err := reader.ReadOrderLabelTask(ctx, work.Scope, taskID)
	if errors.Is(err, orders.ErrSchemaDrift) {
		return r.deferReadback(ctx, work, "LABEL_SCHEMA_DRIFT")
	}
	if err != nil || task.ID != taskID {
		return r.deferReadback(ctx, work, "LABEL_TASK_READBACK_UNAVAILABLE")
	}
	if task.Code == "error" || task.ErrorCode != "" {
		return r.recordLabelProgress(ctx, work, task, orders.StateRejected, "LABEL_TASK_FAILED")
	}
	if task.Code != "completed" {
		return r.recordLabelProgress(ctx, work, task, work.State, "LABEL_TASK_PENDING")
	}
	if !orders.LabelTaskComplete(work.Command, work.Receipt, task) {
		return r.recordLabelProgress(ctx, work, task, orders.StateUnknown, "LABEL_TARGET_COUNTS_UNVERIFIED")
	}
	return r.saveCompletedLabel(ctx, work, task, client)
}

func (r *Runner) recordLabelProgress(ctx context.Context, work orders.Work, task orders.LabelTask, state orders.State, code string) error {
	receipt := work.Receipt
	receipt.LabelTask = &task
	receipt.State = state
	return r.complete(ctx, work, orders.Completion{State: state, Code: code, Receipt: receipt, NextRunAt: r.now().Add(r.options.PollInterval)})
}

func (r *Runner) saveCompletedLabel(ctx context.Context, work orders.Work, task orders.LabelTask, client orders.Provider) error {
	if r.options.LabelSealer == nil || task.FileURL == "" {
		return r.deferReadback(ctx, work, "LABEL_STORAGE_UNAVAILABLE")
	}
	data, err := r.downloadLabel(ctx, client, work.Scope, task.FileURL)
	if err != nil {
		return r.deferReadback(ctx, work, "LABEL_DOWNLOAD_PENDING")
	}
	labelType := work.Command.PostingAction.Parameters.LabelType
	artifact, err := r.options.LabelSealer.SealOrderLabel(work, orders.ActionTask{ID: task.ID, Type: labelType}, data, r.now())
	if err != nil {
		return r.deferReadback(ctx, work, "LABEL_STORAGE_UNAVAILABLE")
	}
	if err = r.applyLabelRetention(ctx, work, &artifact); err != nil {
		return r.deferReadback(ctx, work, "LABEL_POLICY_CHANGED")
	}

	receipt := work.Receipt
	receipt.State, receipt.LabelTask = orders.StateReconciled, &task
	receipt.Artifact = &orders.OrderArtifact{Available: true, DownloadURL: orders.LabelArtifactURL(work.ID), LabelType: labelType, ExpiresAt: artifact.ExpiresAt}
	return r.complete(ctx, work, orders.Completion{State: orders.StateReconciled, Code: "LABEL_STORED", Receipt: receipt, LabelArtifact: &artifact})
}

func (r *Runner) downloadLabel(ctx context.Context, client orders.Provider, scope orders.Scope, rawURL string) ([]byte, error) {
	if reader, ok := client.(orders.LabelPDFReader); ok {
		return reader.DownloadOrderLabel(ctx, scope, rawURL)
	}
	if r.options.LabelDownloader == nil {
		return nil, orders.ErrBlocked
	}
	return r.options.LabelDownloader(ctx, rawURL)
}

func (r *Runner) applyLabelRetention(ctx context.Context, work orders.Work, artifact *orders.LabelArtifact) error {
	reader, ok := r.store.(interface {
		OrderConfig(context.Context, orders.Scope) (orders.Config, error)
	})
	if !ok {
		return orders.ErrBlocked
	}
	config, err := reader.OrderConfig(ctx, work.Scope)
	if err != nil {
		return err
	}
	if config.Scope != work.Scope || config.Revision != work.ConfigRevision {
		return orders.ErrStale
	}
	deadline := artifact.CreatedAt.Add(config.LabelRetention())
	if artifact.ExpiresAt.After(deadline) {
		artifact.ExpiresAt = deadline
	}
	return nil
}
