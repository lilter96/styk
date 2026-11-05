package orderflow

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

type NativeDocumentCandidateReader interface {
	NativeDocumentCandidates(context.Context, orders.Scope, int, string) (orders.NativeDocumentPointCandidates, error)
}

func (i *Ingestor) readNativeDocumentPage(ctx context.Context, scope orders.Scope, credentials orders.CredentialRevisions, cursor nativePointCursor) (orders.OrderSourcePage, error) {
	store, ok := i.store.(NativeDocumentCandidateReader)
	if !ok {
		return orders.OrderSourcePage{}, orders.ErrBlocked
	}
	candidates, err := store.NativeDocumentCandidates(ctx, scope, i.options.NativeBatch, cursor.DocumentAfter)
	if err != nil {
		return orders.OrderSourcePage{}, err
	}
	if !validNativeDocumentCandidates(scope, candidates, i.options.NativeBatch) {
		return orders.OrderSourcePage{}, orders.ErrInvalid
	}
	page := orders.OrderSourcePage{NativeDocumentReads: make([]orders.NativeDocumentPointRead, 0, len(candidates.Documents)), NextCursor: candidates.NextCursor, Complete: true}
	if len(candidates.Documents) > 0 {
		client, e := resolveOrderClient(ctx, i.options.NativeResolver, scope, "moysklad", credentials.Moysklad)
		if e != nil {
			return page, e
		}
		reader, supported := client.(orders.NativeDocumentReader)
		if !supported {
			return page, orders.ErrBlocked
		}
		i.collectNativeDocuments(ctx, reader, credentials.Moysklad, candidates.Documents, &page)
	}
	page.NextCursor, err = cursor.next(page.NextCursor, true)
	page.Exhausted = page.NextCursor == ""
	page.Complete = page.Complete && page.Exhausted
	return page, err
}

func validNativeDocumentCandidates(scope orders.Scope, candidates orders.NativeDocumentPointCandidates, limit int) bool {
	if len(candidates.Documents) > limit || len(candidates.Documents) == 0 && candidates.NextCursor != "" {
		return false
	}
	seen, documents := make(map[string]bool), make(map[string]bool)
	for _, candidate := range candidates.Documents {
		key := candidate.Correlation.Kind + "/" + candidate.Correlation.DocumentID
		if candidate.WorkID == "" || len(candidate.WorkID) > 512 || candidate.Parent.Validate() != nil || candidate.Parent.Scope != scope || candidate.Parent.CustomerOrderID == "" || candidate.Correlation.Validate() != nil || candidate.Correlation.DocumentID == "" || seen[candidate.WorkID] || documents[key] {
			return false
		}
		seen[candidate.WorkID], documents[key] = true, true
	}
	return candidates.NextCursor == "" || candidates.NextCursor == candidates.Documents[len(candidates.Documents)-1].WorkID
}

func (i *Ingestor) collectNativeDocuments(ctx context.Context, reader orders.NativeDocumentReader, revision int64, candidates []orders.NativeDocumentPointCandidate, page *orders.OrderSourcePage) {
	for index, candidate := range candidates {
		read, stop := i.readNativeDocumentPoint(ctx, reader, revision, candidate)
		page.NativeDocumentReads = append(page.NativeDocumentReads, read)
		if read.Code == "" {
			continue
		}
		page.Complete = false
		page.NativeDocumentFailures = append(page.NativeDocumentFailures, orders.NativeDocumentReadFailure{PostingNumber: candidate.Parent.PostingNumber, WorkID: candidate.WorkID, Code: read.Code})
		if stop {
			if index < len(candidates)-1 {
				page.NextCursor = candidate.WorkID
			}
			break
		}
	}
}

func (i *Ingestor) readNativeDocumentPoint(ctx context.Context, reader orders.NativeDocumentReader, revision int64, candidate orders.NativeDocumentPointCandidate) (orders.NativeDocumentPointRead, bool) {
	read := orders.NativeDocumentPointRead{WorkID: candidate.WorkID, Parent: candidate.Parent, Correlation: candidate.Correlation, CredentialRevision: revision, StartedAt: i.options.Now()}
	actual, err := reader.ReadNativeDocument(ctx, orders.NativeDocumentRequest{Scope: candidate.Parent.Scope, Correlation: candidate.Correlation, IncludeReturns: candidate.Correlation.Kind == "demand"})
	read.FinishedAt = i.options.Now()
	if err != nil {
		code, stop := nativePointReadFailure(err)
		read.Code = code
		return read, stop
	}
	read.Observation = &actual
	if read.Validate() != nil {
		read.Observation, read.Code = nil, "NATIVE_ORDER_INCOMPLETE"
	}
	return read, false
}
