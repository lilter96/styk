//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/stockfixture"
)

func replacementPath(id string) string { return "/api/product-mapping-reviews/" + id + "/replace" }

func confirmedForReplacement(t *testing.T, h *confirmationHarness) domain.MappingConfirmationResult {
	t.Helper()
	r := h.createReview(t, "candidate-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	return parseConfirmation(t, h.post(confirmationPath(r.Review.ID), confirmationBody("initial", "initial pair"), h.cookie))
}

func TestMappingReplacementPreservesHistoryAndReplaysExactOutcome(t *testing.T) {
	h := newConfirmationHarness(t)
	first := confirmedForReplacement(t, h)
	r := h.createReview(t, "candidate-product", "product", "candidate-item", domain.MappingOperatorAttested)
	if !r.ReplacementAllowed || !r.MappingConflict {
		t.Fatal("changed pair was not eligible for explicit replacement")
	}
	body := confirmationBody("replace", "operator checked both products")
	if w := h.post(confirmationPath(r.Review.ID), body, h.cookie); w.Code != http.StatusConflict {
		t.Fatal("ordinary confirmation replaced existing mapping")
	}
	result := parseConfirmation(t, h.post(replacementPath(r.Review.ID), body, h.cookie))
	if result.MappingID != first.MappingID || result.MappingVersion != first.MappingVersion+1 {
		t.Fatal("replacement did not append same mapping's next version")
	}
	replay := parseConfirmation(t, h.post(replacementPath(r.Review.ID), body, h.cookie))
	if !replay.IdempotentReplay || replay.MappingVersion != result.MappingVersion || !replay.ConfirmedAt.Equal(result.ConfirmedAt) {
		t.Fatal("replacement replay changed outcome")
	}
	if w := h.post(confirmationPath(r.Review.ID), body, h.cookie); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "IDEMPOTENCY_CONFLICT") {
		t.Fatal("same key allowed a different confirmation mode")
	}
	h.assertRecords(t, "candidate-product", 1, 2, 2, 2)
	var old, current string
	err := h.f.store.Pool.QueryRow(context.Background(), `SELECT h.target_id,m.target_id FROM product_mapping_history h JOIN product_mappings m ON m.id=h.mapping_id WHERE h.mapping_id=$1 AND h.version=$2`, first.MappingID, first.MappingVersion).Scan(&old, &current)
	if err != nil || old != "manual-variant" || current != "candidate-item" {
		t.Fatalf("old/current pair lost: %s/%s, %v", old, current, err)
	}
}

func TestMappingReplacementConcurrentReviewsOnlyOneWins(t *testing.T) {
	h := newConfirmationHarness(t)
	confirmedForReplacement(t, h)
	a := h.createReview(t, "candidate-product", "product", "candidate-item", domain.MappingOperatorAttested)
	b := h.createReview(t, "candidate-product", "product", "alternate-item", domain.MappingOperatorAttested)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, r := range []domain.MappingReviewResult{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- h.post(replacementPath(r.Review.ID), confirmationBody("compete", "reviewed"), h.cookie).Code
		}()
	}
	wg.Wait()
	close(codes)
	success, stale := 0, 0
	for code := range codes {
		if code == http.StatusOK {
			success++
		} else if code == http.StatusConflict {
			stale++
		} else {
			t.Fatalf("unexpected replacement status %d", code)
		}
	}
	if success != 1 || stale != 1 {
		t.Fatalf("wins/stale=%d/%d", success, stale)
	}
	h.assertRecords(t, "candidate-product", 1, 2, 2, 2)
}

func TestMappingReplacementRollsBackIncompleteAudit(t *testing.T) {
	h := newConfirmationHarness(t)
	initial := confirmedForReplacement(t, h)
	r := h.createReview(t, "candidate-product", "product", "candidate-item", domain.MappingOperatorAttested)
	_, err := h.f.store.Pool.Exec(context.Background(), `ALTER TABLE product_mapping_confirmation_audits ADD CONSTRAINT replacement_test_failure CHECK(explanation<>'replacement-failure') NOT VALID`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := h.f.store.Pool.Exec(context.Background(), `ALTER TABLE product_mapping_confirmation_audits DROP CONSTRAINT IF EXISTS replacement_test_failure`); err != nil {
			t.Error(err)
		}
	})
	w := h.post(replacementPath(r.Review.ID), confirmationBody("failed", "replacement-failure"), h.cookie)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed audit status=%d", w.Code)
	}
	h.assertRecords(t, "candidate-product", 1, 1, 1, 1)
	var version, replacements int
	if err := h.f.store.Pool.QueryRow(context.Background(), `SELECT version,(SELECT count(*) FROM product_mapping_replacements WHERE mapping_id=$1) FROM product_mappings WHERE id=$1`, initial.MappingID).Scan(&version, &replacements); err != nil || version != initial.MappingVersion || replacements != 0 {
		t.Fatalf("partial replacement: %d/%d, %v", version, replacements, err)
	}
}

func TestProductConflictFilterIncludesDifferentArticleCandidate(t *testing.T) {
	h := newConfirmationHarness(t)
	confirmedForReplacement(t, h)
	path := fmt.Sprintf("/api/products?tenant=%s&ozon_account=%s&moysklad_account=%s&mapping=conflict&search=candidate-product", h.f.req.Pool.Tenant, h.f.req.Target.OzonAccount, h.f.req.Pool.MoyskladAccount)
	w := h.call(http.MethodGet, path, "", "", "127.0.0.1:8080", "127.0.0.1:1234", h.cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"DIFFERENT_CANDIDATE"`) || !strings.Contains(w.Body.String(), `"product_id":"candidate-product"`) {
		t.Fatalf("candidate conflict missing from filter: %d %s", w.Code, w.Body.String())
	}
}

func TestMappingReplacementRejectsPendingPublication(t *testing.T) {
	db, publication := controlledFixture(t)
	controlledEnable(t, db, publication)
	ctx := context.Background()
	scope := stockfixture.Scope()
	now := time.Now().UTC()
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now, FinishedAt: now,
		Entities: []domain.ExternalEntity{{Kind: "product", ID: stockfixture.SourceProduct}, {Kind: "product", ID: "replacement-target"}, {Kind: "store", ID: stockfixture.SourceWarehouse}}}
	if _, err := db.SaveInventoryImport(ctx, source, "replacement-active-publication"); err != nil {
		t.Fatal(err)
	}
	r, err := db.CreateMappingReview(ctx, domain.MappingReviewRequest{Tenant: scope.Tenant, OzonAccount: scope.OzonAccount, MoyskladAccount: scope.MoyskladAccount, ProductID: stockfixture.OzonProduct, TargetKind: "product", TargetID: "replacement-target", Evidence: domain.MappingOperatorAttested, SessionID: "synthetic-session", Lifetime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ConfirmMappingReview(ctx, domain.MappingConfirmationRequest{ReviewID: r.Review.ID, SessionID: "synthetic-session", IdempotencyKey: "pending-publication", Acknowledged: true, ReplaceExisting: true})
	if !errors.Is(err, postgres.ErrMappingBusy) {
		t.Fatalf("pending publication allowed replacement: %v", err)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM product_mapping_replacements`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("blocked replacement wrote history: %d, %v", count, err)
	}
}

func TestMappingReplacementRequiresReviewSessionAndOrigin(t *testing.T) {
	h := newConfirmationHarness(t)
	confirmedForReplacement(t, h)
	r := h.createReview(t, "candidate-product", "product", "candidate-item", domain.MappingOperatorAttested)
	path, body := replacementPath(r.Review.ID), confirmationBody("guarded", "checked")
	if w := h.post(path, body, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated replace=%d", w.Code)
	}
	if w := h.call(http.MethodPost, path, body, "http://untrusted.invalid", "127.0.0.1:8080", "127.0.0.1:1234", h.cookie); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin replace=%d", w.Code)
	}
	if w := h.post(path, body, h.login(t)); w.Code != http.StatusNotFound {
		t.Fatalf("other session replaced review: %d", w.Code)
	}
	h.assertRecords(t, "candidate-product", 1, 1, 1, 1)
}
