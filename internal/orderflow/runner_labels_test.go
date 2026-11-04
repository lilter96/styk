package orderflow

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orderprivacy"
	"github.com/lilter96/styk/internal/orders"
)

type labelReaderFixture struct {
	*flowProvider
	task  orders.LabelTask
	polls int
}

func (p *labelReaderFixture) ReadOrderLabelTask(context.Context, orders.Scope, string) (orders.LabelTask, error) {
	p.polls++
	return p.task, nil
}

type labelResolverFixture struct {
	inner  orders.Resolver
	source orders.Provider
}

func (r labelResolverFixture) ResolveOrder(ctx context.Context, scope orders.Scope, provider, account string, revision int64) (orders.Binding, error) {
	b, err := r.inner.ResolveOrder(ctx, scope, provider, account, revision)
	if err == nil && provider == "ozon" {
		b.Client = r.source
	}
	return b, err
}

func TestLabelCompletionRequiresStorageAndExactCounts(t *testing.T) {
	t.Parallel()
	store, source, _, runner := dispatchedFlow(t)
	reader := prepareLabelReadback(t, store, source, runner)
	store.review.Config.PIIRetentionSeconds = 3600
	downloads := 0
	runner.options.LabelDownloader = func(context.Context, string) ([]byte, error) {
		downloads++
		return []byte("%PDF-1.7\nPRIVATE\n%%EOF"), nil
	}
	runFlow(t, runner)
	if store.work.State == orders.StateReconciled || downloads != 0 {
		t.Fatal("missing storage reconciled")
	}
	vault, err := credentials.New(bytes.Repeat([]byte{29}, 32))
	if err != nil {
		t.Fatal(err)
	}
	runner.options.LabelSealer = orderprivacy.LabelProtector{Vault: vault}
	reader.task.CountsKnown = false
	runFlow(t, runner)
	if store.work.State != orders.StateUnknown || downloads != 0 {
		t.Fatal("missing counts became zero or complete")
	}
	reader.task.CountsKnown = true
	runFlow(t, runner)
	last := store.completions[len(store.completions)-1]
	if store.work.State != orders.StateReconciled || downloads != 1 || last.LabelArtifact == nil || last.Receipt.Artifact == nil || !last.Receipt.Artifact.Available {
		t.Fatal("PDF not atomically included in reconciliation")
	}
	if last.LabelArtifact.ExpiresAt.Sub(last.LabelArtifact.CreatedAt) != time.Hour {
		t.Fatal("shorter privacy policy ignored")
	}
	if reader.writes != 0 {
		t.Fatal("label create replayed during polling")
	}
}

func prepareLabelReadback(t *testing.T, store *flowStore, source *flowProvider, runner *Runner) *labelReaderFixture {
	t.Helper()
	// Reuse the already scoped capability fixture, then freeze a label action.
	prepareSplitReadback(t, store, source)
	w := store.work
	w.Command.Action = orders.ActionPostingLabelCreate
	w.Command.PostingAction.Parameters = orders.ActionParameters{LabelType: "big_label"}
	w.Receipt = orders.Receipt{CommandID: w.Command.ID, State: orders.StatePolling, TaskID: "99", TaskIDs: []orders.ActionTask{{ID: "99", Type: "big_label"}}, LabelTasksVerified: true, ObservedAt: flowTestNow}
	w.State = orders.StatePolling
	var err error
	w.CommandFingerprint, err = orders.CommandFingerprint(w.Command)
	if err != nil {
		t.Fatal(err)
	}
	preview := store.previews[w.PreviewID]
	preview.Decisions[0].Commands = []orders.Command{w.Command}
	store.previews[w.PreviewID] = preview
	reader := &labelReaderFixture{flowProvider: source, task: orders.LabelTask{ID: "99", Code: "completed", CountsKnown: true, PostingsCount: 1, PrintedPostingsCount: 1, FileURL: "https://api-seller.ozon.ru/label.pdf"}}
	runner.resolver = labelResolverFixture{inner: runner.resolver, source: reader}
	return reader
}
