//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/commitment"
	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/matching"
	"github.com/lilter96/styk/internal/postgres"
)

const confirmationTestOrigin = "http://127.0.0.1:5173"
const confirmationTestSecret = "synthetic-test-operator-secret-123456789"

type confirmationHarness struct {
	f      stockFixtureData
	h      http.Handler
	cookie *http.Cookie
}

// The fixture uses only sanitized imports in the dedicated PostgreSQL database.
// Its original legacy mapping is retained to verify compatibility and isolation.
func newConfirmationHarness(t *testing.T) *confirmationHarness {
	t.Helper()
	f := newStockFixture(t)
	now := time.Now().UTC()
	f.source.Entities = append(f.source.Entities,
		domain.ExternalEntity{Kind: "product", ID: "candidate-item", Article: "candidate-offer", Code: "different-code"},
		domain.ExternalEntity{Kind: "product", ID: "unique-item", Article: "unique-offer", Code: "unique-code"},
		domain.ExternalEntity{Kind: "variant", ID: "manual-variant", Article: "source-article", Code: "source-code"},
		domain.ExternalEntity{Kind: "product", ID: "alternate-item", Article: "candidate-offer", Code: "alternate-code"},
	)
	stock, reserve, incoming := "10", "2", "0"
	f.source.WarehouseStock = append(f.source.WarehouseStock, domain.WarehouseStockObservation{Kind: "variant", ExternalID: "manual-variant", WarehouseID: "warehouse", Stock: &stock, Reserve: &reserve, InTransit: &incoming})
	f.source.StartedAt = now.Add(-time.Second)
	f.source.FinishedAt = now
	if _, err := f.store.SaveInventoryImport(context.Background(), f.source, "confirmation-fixture-moy"); err != nil {
		t.Fatal(err)
	}
	f.ozon.Products = append(f.ozon.Products,
		domain.OzonProduct{ProductID: "candidate-product", OfferID: "candidate-offer"},
		domain.OzonProduct{ProductID: "unique-product", OfferID: "unique-offer"},
		domain.OzonProduct{ProductID: "manual-product", OfferID: "seller-offer"},
		domain.OzonProduct{ProductID: "second-offer", OfferID: "other-offer"},
	)
	f.ozon.Stocks = append(f.ozon.Stocks, domain.OzonStock{ProductID: "manual-product", OfferID: "seller-offer", WarehouseID: "ozon-warehouse", Present: 8, Reserved: 1, FreeStock: 7})
	f.ozon.StartedAt = now.Add(-time.Second)
	f.ozon.FinishedAt = now
	if _, err := f.store.SaveOzonImport(context.Background(), f.ozon, "confirmation-fixture-ozon"); err != nil {
		t.Fatal(err)
	}
	handler, err := console.Handler(f.store, confirmationTestSecret, confirmationTestOrigin)
	if err != nil {
		t.Fatal(err)
	}
	h := &confirmationHarness{f: f, h: handler}
	h.cookie = h.login(t)
	return h
}

func (h *confirmationHarness) call(method, path, body, origin, host, remote string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
	r.Host = host
	r.RemoteAddr = remote
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.h.ServeHTTP(w, r)
	return w
}
func (h *confirmationHarness) post(path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	return h.call("POST", path, body, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", cookie)
}
func (h *confirmationHarness) login(t *testing.T) *http.Cookie {
	t.Helper()
	w := h.post("/api/session", fmt.Sprintf(`{"secret":%q}`, confirmationTestSecret), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("login status: %d", w.Code)
	}
	return w.Result().Cookies()[0]
}
func (h *confirmationHarness) createReview(t *testing.T, product, kind, target string, evidence domain.MappingEvidenceClass) domain.MappingReviewResult {
	t.Helper()
	body := fmt.Sprintf(`{"tenant":%q,"ozon_account":%q,"moysklad_account":%q,"product_id":%q,"target_kind":%q,"target_id":%q,"evidence_class":%q}`, h.f.req.Pool.Tenant, h.f.req.Target.OzonAccount, h.f.req.Pool.MoyskladAccount, product, kind, target, evidence)
	w := h.post("/api/product-mapping-reviews", body, h.cookie)
	if w.Code != http.StatusCreated {
		t.Fatalf("review creation status %d: %s", w.Code, w.Body.String())
	}
	var out domain.MappingReviewResult
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func confirmationBody(key, explanation string) string {
	return fmt.Sprintf(`{"idempotency_key":%q,"acknowledge":true,"explanation":%q}`, key, explanation)
}
func confirmationPath(id string) string { return "/api/product-mapping-reviews/" + id + "/confirm" }
func parseConfirmation(t *testing.T, w *httptest.ResponseRecorder) domain.MappingConfirmationResult {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("confirmation status %d: %s", w.Code, w.Body.String())
	}
	var out domain.MappingConfirmationResult
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func (h *confirmationHarness) assertRecords(t *testing.T, product string, mappings, history, audits, outcomes int) {
	t.Helper()
	var gotM, gotH, gotA, gotI int
	err := h.f.store.Pool.QueryRow(context.Background(), `SELECT
 (SELECT count(*) FROM product_mappings WHERE tenant_id=$1 AND ozon_account=$2 AND ozon_product_id=$3),
 (SELECT count(*) FROM product_mapping_history WHERE tenant_id=$1 AND ozon_account=$2 AND ozon_product_id=$3),
 (SELECT count(*) FROM product_mapping_confirmation_audits WHERE tenant_id=$1 AND ozon_account=$2 AND ozon_product_id=$3),
 (SELECT count(*) FROM product_mapping_confirmation_idempotency i JOIN product_mapping_confirmation_audits a ON a.mapping_id=i.mapping_id AND a.mapping_version=i.mapping_version WHERE a.tenant_id=$1 AND a.ozon_account=$2 AND a.ozon_product_id=$3)`, h.f.req.Pool.Tenant, h.f.req.Target.OzonAccount, product).Scan(&gotM, &gotH, &gotA, &gotI)
	if err != nil {
		t.Fatal(err)
	}
	if gotM != mappings || gotH != history || gotA != audits || gotI != outcomes {
		t.Fatalf("unexpected record counts %d/%d/%d/%d", gotM, gotH, gotA, gotI)
	}
}
func (h *confirmationHarness) sessionID(t *testing.T, reviewID string) string {
	t.Helper()
	var id string
	if err := h.f.store.Pool.QueryRow(context.Background(), `SELECT session_audit_id FROM product_mapping_reviews WHERE id=$1`, reviewID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMappingConfirmationSuccessEvidenceAndReplay(t *testing.T) {
	cases := []struct {
		name, product, kind, target string
		evidence                    domain.MappingEvidenceClass
	}{
		{"unique deterministic product", "unique-product", "product", "unique-item", domain.MappingDeterministicCandidate},
		{"ambiguous deterministic product", "candidate-product", "product", "candidate-item", domain.MappingDeterministicCandidate},
		{"manual variant with different article and code", "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newConfirmationHarness(t)
			if tc.name == "ambiguous deterministic product" {
				scope := postgres.MatchScope{Tenant: h.f.req.Pool.Tenant, OzonAccount: h.f.req.Target.OzonAccount, MoyskladAccount: h.f.req.Pool.MoyskladAccount}
				data, err := h.f.store.MatchData(context.Background(), scope)
				if err != nil {
					t.Fatal(err)
				}
				var found bool
				for _, source := range data.Sources {
					if source.Identity.ID == tc.product {
						r := matching.New(data.Targets, data.Mappings).Match(scope.Tenant, source, scope.MoyskladAccount)
						found = r.Status == matching.MultipleCandidates && len(r.Candidates) == 2
					}
				}
				if !found {
					t.Fatal("expected two distinct candidates before exact selection")
				}
			}
			review := h.createReview(t, tc.product, tc.kind, tc.target, tc.evidence)
			if review.MappingConflict || review.Review.EvidenceClass != tc.evidence {
				t.Fatal("incorrect review")
			}
			var outboxBefore, decisionsBefore int
			if err := h.f.store.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM outbox_tasks),(SELECT count(*) FROM stock_decisions WHERE tenant_id=$1)`, h.f.req.Pool.Tenant).Scan(&outboxBefore, &decisionsBefore); err != nil {
				t.Fatal(err)
			}
			body := confirmationBody("retry-key-1", "synthetic operator attestation")
			first := parseConfirmation(t, h.post(confirmationPath(review.Review.ID), body, h.cookie))
			if first.MappingID <= 0 || first.MappingVersion != 1 || first.ReviewID != review.Review.ID || first.EvidenceClass != tc.evidence || first.ConfirmedAt.IsZero() || first.IdempotentReplay {
				t.Fatal("incorrect confirmation result")
			}
			// Discarding the first HTTP result simulates a lost response; the same key recovers it.
			second := parseConfirmation(t, h.post(confirmationPath(review.Review.ID), body, h.cookie))
			if !second.IdempotentReplay || second.MappingID != first.MappingID || second.MappingVersion != first.MappingVersion || !second.ConfirmedAt.Equal(first.ConfirmedAt) || second.EvidenceClass != first.EvidenceClass {
				t.Fatal("replay returned a different result")
			}
			h.assertRecords(t, tc.product, 1, 1, 1, 1)
			changed := h.post(confirmationPath(review.Review.ID), confirmationBody("retry-key-1", "different explanation"), h.cookie)
			if changed.Code != 409 || !strings.Contains(changed.Body.String(), `"IDEMPOTENCY_CONFLICT"`) {
				t.Fatal("idempotency collision not rejected")
			}
			falseAck := h.post(confirmationPath(review.Review.ID), `{"idempotency_key":"retry-key-1","acknowledge":false,"explanation":"synthetic operator attestation"}`, h.cookie)
			if falseAck.Code != 409 || !strings.Contains(falseAck.Body.String(), `"IDEMPOTENCY_CONFLICT"`) {
				t.Fatal("changed acknowledgement did not conflict")
			}
			var kind, target, evidence, principal, auditSession, explanation, provenance, reviewID string
			var oi, mi int64
			var confirmed time.Time
			var version int
			err := h.f.store.Pool.QueryRow(context.Background(), `SELECT a.target_kind,a.target_id,a.evidence_class,a.principal_type,a.session_audit_id,a.explanation,m.provenance,a.review_id,a.ozon_import_id,a.moysklad_import_id,a.confirmed_at,a.mapping_version FROM product_mapping_confirmation_audits a JOIN product_mappings m ON m.id=a.mapping_id WHERE a.mapping_id=$1`, first.MappingID).Scan(&kind, &target, &evidence, &principal, &auditSession, &explanation, &provenance, &reviewID, &oi, &mi, &confirmed, &version)
			if err != nil {
				t.Fatal(err)
			}
			if kind != tc.kind || target != tc.target || evidence != string(tc.evidence) || principal != "LOCAL_OPERATOR_SESSION" || auditSession != h.sessionID(t, review.Review.ID) || auditSession == h.cookie.Value || strings.Contains(explanation, confirmationTestSecret) || explanation != "synthetic operator attestation" || provenance != "LOCAL_OPERATOR_SESSION" || reviewID != review.Review.ID || oi != review.Review.OzonImportID || mi != review.Review.MoyskladImportID || !confirmed.Equal(first.ConfirmedAt) || version != 1 {
				t.Fatal("audit fields do not match review and result")
			}
			if strings.Contains(h.post(confirmationPath(review.Review.ID), body, h.cookie).Body.String(), auditSession) || strings.Contains(h.post(confirmationPath(review.Review.ID), body, h.cookie).Body.String(), h.cookie.Value) {
				t.Fatal("private session identity leaked")
			}
			// A later completed traversal must not hide a committed outcome.
			next := h.f.ozon
			next.Products = append(next.Products, domain.OzonProduct{ProductID: "after-confirmation", OfferID: "later-offer"})
			next.StartedAt = time.Now()
			next.FinishedAt = time.Now().Add(time.Second)
			if _, err = h.f.store.SaveOzonImport(context.Background(), next, "confirmation-replay-after-import"); err != nil {
				t.Fatal(err)
			}
			replayAfterImport := parseConfirmation(t, h.post(confirmationPath(review.Review.ID), body, h.cookie))
			if !replayAfterImport.IdempotentReplay || replayAfterImport.MappingID != first.MappingID {
				t.Fatal("committed result was hidden by a later import")
			}
			var outboxAfter, decisionsAfter int
			if err = h.f.store.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM outbox_tasks),(SELECT count(*) FROM stock_decisions WHERE tenant_id=$1)`, h.f.req.Pool.Tenant).Scan(&outboxAfter, &decisionsAfter); err != nil {
				t.Fatal(err)
			}
			if outboxBefore != outboxAfter || decisionsBefore != decisionsAfter {
				t.Fatal("confirmation created an external task or stock decision")
			}
		})
	}
}

func TestMappingConfirmationSessionOriginAndScope(t *testing.T) {
	h := newConfirmationHarness(t)
	review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	path := confirmationPath(review.Review.ID)
	valid := confirmationBody("security-key", "synthetic")
	otherSession := h.login(t)
	cases := []struct {
		name, body, origin, host, remote string
		cookie                           *http.Cookie
		status                           int
		code                             string
	}{
		{"missing session", valid, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", nil, 401, "UNAUTHORIZED"},
		{"invalid cookie", valid, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", &http.Cookie{Name: h.cookie.Name, Value: "invalid"}, 401, "UNAUTHORIZED"},
		{"different session", valid, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", otherSession, 404, "REVIEW_NOT_FOUND"},
		{"invalid origin", valid, "http://example.invalid", "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 403, "FORBIDDEN"},
		{"missing origin", valid, "", "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 403, "FORBIDDEN"},
		{"invalid host", valid, confirmationTestOrigin, "example.invalid:8080", "127.0.0.1:1234", h.cookie, 403, "FORBIDDEN"},
		{"remote peer", valid, confirmationTestOrigin, "127.0.0.1:8080", "192.0.2.1:2222", h.cookie, 403, "FORBIDDEN"},
		{"missing acknowledgement", `{"idempotency_key":"security-key"}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"negative acknowledgement", `{"idempotency_key":"security-key","acknowledge":false}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"fake principal", `{"idempotency_key":"security-key","acknowledge":true,"principal":"human"}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"fake session", `{"idempotency_key":"security-key","acknowledge":true,"session_audit_id":"fake"}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"fake tenant", `{"idempotency_key":"security-key","acknowledge":true,"tenant":"other"}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"fake account", `{"idempotency_key":"security-key","acknowledge":true,"ozon_account":"other"}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"fake import", `{"idempotency_key":"security-key","acknowledge":true,"ozon_import_id":123}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"fake evidence", `{"idempotency_key":"security-key","acknowledge":true,"evidence_class":"INDEPENDENTLY_VERIFIED"}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"invalid key", `{"idempotency_key":"bad key","acknowledge":true}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"large request", fmt.Sprintf(`{"idempotency_key":"security-key","acknowledge":true,"explanation":%q}`, strings.Repeat("x", 5000)), confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"multiple json values", valid + ` {}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := h.call("POST", path, tc.body, tc.origin, tc.host, tc.remote, tc.cookie)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), `"`+tc.code+`"`) {
				t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), confirmationTestSecret) || strings.Contains(w.Body.String(), h.cookie.Value) {
				t.Fatal("credential exposed")
			}
		})
	}
	h.assertRecords(t, "manual-product", 0, 0, 0, 0)
	// A caller cannot smuggle a different account or tenant into a confirmation.
	badScope := fmt.Sprintf(`{"tenant":%q,"ozon_account":%q,"moysklad_account":%q,"product_id":"manual-product","target_kind":"variant","target_id":"manual-variant","evidence_class":"OPERATOR_ATTESTED"}`, h.f.req.Pool.Tenant+"-other", h.f.req.Target.OzonAccount, h.f.req.Pool.MoyskladAccount)
	if w := h.post("/api/product-mapping-reviews", badScope, h.cookie); w.Code != 404 {
		t.Fatal("cross-tenant review accepted")
	}
	badScope = fmt.Sprintf(`{"tenant":%q,"ozon_account":%q,"moysklad_account":%q,"product_id":"manual-product","target_kind":"variant","target_id":"manual-variant","evidence_class":"OPERATOR_ATTESTED"}`, h.f.req.Pool.Tenant, h.f.req.Target.OzonAccount+"-other", h.f.req.Pool.MoyskladAccount)
	if w := h.post("/api/product-mapping-reviews", badScope, h.cookie); w.Code != 404 {
		t.Fatal("cross-account review accepted")
	}
	// Rejected attempts have no effect on a later properly authorized request.
	parseConfirmation(t, h.post(path, valid, h.cookie))
	h.assertRecords(t, "manual-product", 1, 1, 1, 1)
}

func TestMappingConfirmationPreservesExistingMappings(t *testing.T) {
	h := newConfirmationHarness(t)
	for _, target := range []struct{ kind, id string }{{"variant", "item"}, {"variant", "manual-variant"}} {
		review := h.createReview(t, "product", target.kind, target.id, domain.MappingOperatorAttested)
		w := h.post(confirmationPath(review.Review.ID), confirmationBody("no-replacement", "reviewed"), h.cookie)
		if w.Code != 409 || !strings.Contains(w.Body.String(), `"MAPPING_CONFLICT"`) {
			t.Fatalf("existing mapping was not protected: %d %s", w.Code, w.Body.String())
		}
	}
	h.assertRecords(t, "product", 1, 1, 0, 0)
	var original string
	if err := h.f.store.Pool.QueryRow(context.Background(), `SELECT target_id FROM product_mappings WHERE tenant_id=$1 AND ozon_account=$2 AND ozon_product_id='product'`, h.f.req.Pool.Tenant, h.f.req.Target.OzonAccount).Scan(&original); err != nil || original != "item" {
		t.Fatal("legacy mapping was changed")
	}
}

func TestMappingConfirmationConcurrentIdenticalRequests(t *testing.T) {
	h := newConfirmationHarness(t)
	review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	const count = 8
	type outcome struct {
		status int
		body   string
		result domain.MappingConfirmationResult
	}
	start := make(chan struct{})
	done := make(chan outcome, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			w := h.post(confirmationPath(review.Review.ID), confirmationBody("same-concurrent-key", "same"), h.cookie)
			var v domain.MappingConfirmationResult
			_ = json.Unmarshal(w.Body.Bytes(), &v)
			done <- outcome{w.Code, w.Body.String(), v}
		}()
	}
	close(start)
	wg.Wait()
	close(done)
	firstCount, replayCount := 0, 0
	var id int64
	for x := range done {
		if x.status != 200 {
			t.Fatalf("concurrent confirmation failed: %d %s", x.status, x.body)
		}
		if id == 0 {
			id = x.result.MappingID
		}
		if x.result.MappingID != id {
			t.Fatal("different mappings created")
		}
		if x.result.IdempotentReplay {
			replayCount++
		} else {
			firstCount++
		}
	}
	if firstCount != 1 || replayCount != count-1 {
		t.Fatalf("unexpected first/replay count: %d/%d", firstCount, replayCount)
	}
	h.assertRecords(t, "manual-product", 1, 1, 1, 1)
}

func TestMappingConfirmationConcurrentDifferentPayloadSameKey(t *testing.T) {
	h := newConfirmationHarness(t)
	review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	start := make(chan struct{})
	done := make(chan *httptest.ResponseRecorder, 2)
	for _, description := range []string{"first operator statement", "second operator statement"} {
		go func(explanation string) {
			<-start
			done <- h.post(confirmationPath(review.Review.ID), confirmationBody("shared-key", explanation), h.cookie)
		}(description)
	}
	close(start)
	a, b := <-done, <-done
	if (a.Code != http.StatusOK || b.Code != http.StatusConflict) && (a.Code != http.StatusConflict || b.Code != http.StatusOK) {
		t.Fatalf("expected one success and one conflict: %d/%d", a.Code, b.Code)
	}
	conflict := a
	if b.Code == 409 {
		conflict = b
	}
	if !strings.Contains(conflict.Body.String(), `"IDEMPOTENCY_CONFLICT"`) {
		t.Fatal("different payload did not conflict")
	}
	h.assertRecords(t, "manual-product", 1, 1, 1, 1)
}

func TestMappingConfirmationConcurrentCompetingReviews(t *testing.T) {
	h := newConfirmationHarness(t)
	a := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	b := h.createReview(t, "manual-product", "product", "candidate-item", domain.MappingOperatorAttested)
	start := make(chan struct{})
	done := make(chan *httptest.ResponseRecorder, 2)
	for _, review := range []domain.MappingReviewResult{a, b} {
		go func(review domain.MappingReviewResult) {
			<-start
			done <- h.post(confirmationPath(review.Review.ID), confirmationBody("competing-key", "synthetic"), h.cookie)
		}(review)
	}
	close(start)
	first, second := <-done, <-done
	if (first.Code != http.StatusOK || second.Code != http.StatusConflict) && (first.Code != http.StatusConflict || second.Code != http.StatusOK) {
		t.Fatalf("competing reviews both succeeded or failed: %d/%d", first.Code, second.Code)
	}
	failed := first
	if second.Code == 409 {
		failed = second
	}
	if !strings.Contains(failed.Body.String(), `"REVIEW_STALE"`) {
		t.Fatal("competing review not reported stale")
	}
	h.assertRecords(t, "manual-product", 1, 1, 1, 1)
}

func TestSeveralOffersCanMapToOneSourceItem(t *testing.T) {
	h := newConfirmationHarness(t)
	first := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	second := h.createReview(t, "second-offer", "variant", "manual-variant", domain.MappingOperatorAttested)
	parseConfirmation(t, h.post(confirmationPath(first.Review.ID), confirmationBody("first-offer-key", "one"), h.cookie))
	parseConfirmation(t, h.post(confirmationPath(second.Review.ID), confirmationBody("second-offer-key", "two"), h.cookie))
	h.assertRecords(t, "manual-product", 1, 1, 1, 1)
	h.assertRecords(t, "second-offer", 1, 1, 1, 1)
}

func TestMappingConfirmationAuditAndHistoryAreImmutable(t *testing.T) {
	h := newConfirmationHarness(t)
	review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	result := parseConfirmation(t, h.post(confirmationPath(review.Review.ID), confirmationBody("immutable-key", "synthetic"), h.cookie))
	for _, q := range []string{
		`UPDATE product_mappings SET target_id='changed' WHERE id=$1`,
		`DELETE FROM product_mappings WHERE id=$1`,
		`UPDATE product_mapping_confirmation_audits SET explanation='changed' WHERE mapping_id=$1`,
		`DELETE FROM product_mapping_confirmation_audits WHERE mapping_id=$1`,
		`UPDATE product_mapping_confirmation_idempotency SET request_hash=repeat('0',64) WHERE mapping_id=$1`,
		`DELETE FROM product_mapping_confirmation_idempotency WHERE mapping_id=$1`,
		`UPDATE product_mapping_history SET provenance='changed' WHERE mapping_id=$1`,
		`DELETE FROM product_mapping_history WHERE mapping_id=$1`,
	} {
		if _, err := h.f.store.Pool.Exec(context.Background(), q, result.MappingID); err == nil {
			t.Fatalf("immutable record accepted mutation: %s", q)
		}
	}
	// Legacy administrative history has no review marker and retains its previous behavior.
	if _, err := h.f.store.Pool.Exec(context.Background(), `UPDATE product_mapping_history SET provenance=provenance WHERE tenant_id=$1 AND ozon_product_id='product'`, h.f.req.Pool.Tenant); err != nil {
		t.Fatalf("legacy history behavior changed: %v", err)
	}
	h.assertRecords(t, "manual-product", 1, 1, 1, 1)
}

func TestReviewBoundHistoryRequiresAuditAndIdempotencyAtCommit(t *testing.T) {
	h := newConfirmationHarness(t)
	review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	ctx := context.Background()
	tx, err := h.f.store.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackLeaseTest(t, tx)
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO product_mappings(tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,version,provenance) VALUES($1,$2,'manual-product','seller-offer',$3,'variant','manual-variant',1,'LOCAL_OPERATOR_SESSION') RETURNING id`, h.f.req.Pool.Tenant, h.f.req.Target.OzonAccount, h.f.req.Pool.MoyskladAccount).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_mapping_history(mapping_id,version,tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,provenance,confirmed_at,ozon_import_id,moysklad_import_id,confirmation_review_id) SELECT id,version,tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,provenance,confirmed_at,$2,$3,$4 FROM product_mappings WHERE id=$1`, id, review.Review.OzonImportID, review.Review.MoyskladImportID, review.Review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err == nil {
		t.Fatal("review-bound mapping committed without audit and idempotency")
	}
	h.assertRecords(t, "manual-product", 0, 0, 0, 0)
	parseConfirmation(t, h.post(confirmationPath(review.Review.ID), confirmationBody("after-rollback", "synthetic"), h.cookie))
	h.assertRecords(t, "manual-product", 1, 1, 1, 1)
}

func TestMappingConfirmationRollsBackOnEachPersistenceFailure(t *testing.T) {
	cases := []struct{ name, table, constraint, condition, key, explanation string }{
		{"mapping insert", "product_mappings", "confirm_test_mapping_failure", `ozon_product_id <> 'manual-product'`, "mapping-fail", "synthetic"},
		{"audit insert", "product_mapping_confirmation_audits", "confirm_test_audit_failure", `explanation <> 'synthetic-audit-failure'`, "audit-fail", "synthetic-audit-failure"},
		{"idempotency insert", "product_mapping_confirmation_idempotency", "confirm_test_outcome_failure", `idempotency_key <> 'outcome-fail'`, "outcome-fail", "synthetic"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newConfirmationHarness(t)
			review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
			alter := fmt.Sprintf(`ALTER TABLE %s ADD CONSTRAINT %s CHECK (%s) NOT VALID`, tc.table, tc.constraint, tc.condition)
			drop := fmt.Sprintf(`ALTER TABLE %s DROP CONSTRAINT IF EXISTS %s`, tc.table, tc.constraint)
			if _, err := h.f.store.Pool.Exec(context.Background(), alter); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := h.f.store.Pool.Exec(context.Background(), drop); err != nil {
					t.Errorf("remove test constraint: %v", err)
				}
			})
			body := confirmationBody(tc.key, tc.explanation)
			w := h.post(confirmationPath(review.Review.ID), body, h.cookie)
			if w.Code != 503 || !strings.Contains(w.Body.String(), `"PERSISTENCE_FAILURE"`) {
				t.Fatalf("unexpected error: %d %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), tc.constraint) || strings.Contains(w.Body.String(), "SQLSTATE") {
				t.Fatal("database details exposed")
			}
			h.assertRecords(t, "manual-product", 0, 0, 0, 0)
			h.assertRecords(t, "product", 1, 1, 0, 0)
			if _, err := h.f.store.Pool.Exec(context.Background(), drop); err != nil {
				t.Fatal(err)
			}
			parseConfirmation(t, h.post(confirmationPath(review.Review.ID), body, h.cookie))
			h.assertRecords(t, "manual-product", 1, 1, 1, 1)
		})
	}
}

func TestMappingConfirmationRejectsStaleReview(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*testing.T, *confirmationHarness)
	}{
		{"new Ozon import", func(t *testing.T, h *confirmationHarness) {
			t.Helper()
			next := h.f.ozon
			next.Products = append(next.Products, domain.OzonProduct{ProductID: "later-product", OfferID: "later-offer"})
			next.StartedAt = time.Now()
			next.FinishedAt = time.Now().Add(time.Second)
			if _, err := h.f.store.SaveOzonImport(context.Background(), next, "confirmation-new-ozon"); err != nil {
				t.Fatal(err)
			}
		}},
		{"new MoySklad import", func(t *testing.T, h *confirmationHarness) {
			t.Helper()
			next := h.f.source
			next.Entities = append(next.Entities, domain.ExternalEntity{Kind: "product", ID: "later-item", Article: "later-article"})
			next.StartedAt = time.Now()
			next.FinishedAt = time.Now().Add(time.Second)
			if _, err := h.f.store.SaveInventoryImport(context.Background(), next, "confirmation-new-moy"); err != nil {
				t.Fatal(err)
			}
		}},
		{"in-place source fingerprint", func(t *testing.T, h *confirmationHarness) {
			t.Helper()
			_, err := h.f.store.Pool.Exec(context.Background(), `UPDATE source_entities SET article='changed-source-article' WHERE import_id=(SELECT id FROM source_imports WHERE source='moysklad' AND account_id=$1 ORDER BY id DESC LIMIT 1) AND kind='variant' AND external_id='manual-variant'`, h.f.req.Pool.MoyskladAccount)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"in-place Ozon fingerprint", func(t *testing.T, h *confirmationHarness) {
			t.Helper()
			_, err := h.f.store.Pool.Exec(context.Background(), `UPDATE ozon_product_observations SET sku='changed-synthetic-sku' WHERE import_id=(SELECT id FROM source_imports WHERE source='ozon' AND account_id=$1 ORDER BY id DESC LIMIT 1) AND product_id='manual-product'`, h.f.req.Target.OzonAccount)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"Ozon product missing from later import", func(t *testing.T, h *confirmationHarness) {
			t.Helper()
			next := h.f.ozon
			next.Products = []domain.OzonProduct{next.Products[0]}
			next.Stocks = []domain.OzonStock{next.Stocks[0]}
			next.StartedAt = time.Now()
			next.FinishedAt = time.Now().Add(time.Second)
			if _, err := h.f.store.SaveOzonImport(context.Background(), next, "confirmation-removed-ozon"); err != nil {
				t.Fatal(err)
			}
		}},
		{"MoySklad variant missing from later import", func(t *testing.T, h *confirmationHarness) {
			t.Helper()
			next := h.f.source
			next.Entities = []domain.ExternalEntity{next.Entities[0], next.Entities[1]}
			next.WarehouseStock = []domain.WarehouseStockObservation{next.WarehouseStock[0]}
			next.StartedAt = time.Now()
			next.FinishedAt = time.Now().Add(time.Second)
			if _, err := h.f.store.SaveInventoryImport(context.Background(), next, "confirmation-removed-moy"); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newConfirmationHarness(t)
			review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
			tc.mutate(t, h)
			w := h.post(confirmationPath(review.Review.ID), confirmationBody("stale-key", "synthetic"), h.cookie)
			if w.Code != 409 || !strings.Contains(w.Body.String(), `"REVIEW_STALE"`) {
				t.Fatalf("stale source accepted: %d %s", w.Code, w.Body.String())
			}
			h.assertRecords(t, "manual-product", 0, 0, 0, 0)
			h.assertRecords(t, "product", 1, 1, 0, 0)
		})
	}
}

func TestMappingConfirmationRejectsChangedMappingVersion(t *testing.T) {
	h := newConfirmationHarness(t)
	review := h.createReview(t, "product", "variant", "item", domain.MappingOperatorAttested)
	if _, err := h.f.store.Pool.Exec(context.Background(), `UPDATE product_mappings SET version=version+1 WHERE tenant_id=$1 AND ozon_account=$2 AND ozon_product_id='product'`, h.f.req.Pool.Tenant, h.f.req.Target.OzonAccount); err != nil {
		t.Fatal(err)
	}
	w := h.post(confirmationPath(review.Review.ID), confirmationBody("version-key", "synthetic"), h.cookie)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"REVIEW_STALE"`) {
		t.Fatal("changed mapping version accepted")
	}
	h.assertRecords(t, "product", 1, 1, 0, 0)
}

func TestMappingConfirmationRevalidatesAfterImportLock(t *testing.T) {
	h := newConfirmationHarness(t)
	review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	ctx := context.Background()
	tx, err := h.f.store.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackLeaseTest(t, tx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "ozon:"+h.f.req.Target.OzonAccount); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- h.post(confirmationPath(review.Review.ID), confirmationBody("import-race", "synthetic"), h.cookie)
	}()
	select {
	case <-done:
		t.Fatal("confirmation bypassed importer lock")
	case <-time.After(50 * time.Millisecond):
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO source_imports(source,account_id,fingerprint,started_at,finished_at,report_scope) VALUES('ozon',$1,'confirmation-import-race',now(),now(),'synthetic') RETURNING id`, h.f.req.Target.OzonAccount).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ozon_import_batches(import_id,traversal_status) VALUES($1,'completed')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ozon_product_observations(import_id,product_id,offer_id) VALUES($1,'manual-product','seller-offer')`, id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		if w.Code != 409 || !strings.Contains(w.Body.String(), `"REVIEW_STALE"`) {
			t.Fatalf("concurrent import did not invalidate review: %d %s", w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("confirmation did not resume")
	}
	h.assertRecords(t, "manual-product", 0, 0, 0, 0)
}

func TestMappingConfirmationReplayAfterExpiration(t *testing.T) {
	h := newConfirmationHarness(t)
	seed := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	session := h.sessionID(t, seed.Review.ID)
	input := domain.MappingReviewRequest{Tenant: h.f.req.Pool.Tenant, OzonAccount: h.f.req.Target.OzonAccount, MoyskladAccount: h.f.req.Pool.MoyskladAccount, ProductID: "manual-product", TargetKind: "variant", TargetID: "manual-variant", Evidence: domain.MappingOperatorAttested, SessionID: session, Lifetime: 2 * time.Second}
	review, err := h.f.store.CreateMappingReview(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	body := confirmationBody("expires-after-success", "synthetic")
	first := parseConfirmation(t, h.post(confirmationPath(review.Review.ID), body, h.cookie))
	if wait := time.Until(review.Review.ExpiresAt) + 20*time.Millisecond; wait > 0 {
		time.Sleep(wait)
	}
	replay := parseConfirmation(t, h.post(confirmationPath(review.Review.ID), body, h.cookie))
	if !replay.IdempotentReplay || replay.MappingID != first.MappingID {
		t.Fatal("committed result was lost after expiration")
	}
	otherKey := h.post(confirmationPath(review.Review.ID), confirmationBody("new-after-expiry", "synthetic"), h.cookie)
	if otherKey.Code != 409 || !strings.Contains(otherKey.Body.String(), `"REVIEW_EXPIRED"`) {
		t.Fatal("new request accepted after expiry")
	}
	changed := h.post(confirmationPath(review.Review.ID), confirmationBody("expires-after-success", "different"), h.cookie)
	if changed.Code != 409 || !strings.Contains(changed.Body.String(), `"IDEMPOTENCY_CONFLICT"`) {
		t.Fatal("expired review hid idempotency collision")
	}
	if w := h.post(confirmationPath(review.Review.ID), body, h.login(t)); w.Code != 404 || !strings.Contains(w.Body.String(), `"REVIEW_NOT_FOUND"`) {
		t.Fatal("new session accessed prior outcome")
	}
	h.assertRecords(t, "manual-product", 1, 1, 1, 1)
}

func TestMappingConfirmationExpiredBeforeFirstAttempt(t *testing.T) {
	h := newConfirmationHarness(t)
	seed := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	session := h.sessionID(t, seed.Review.ID)
	in := domain.MappingReviewRequest{Tenant: h.f.req.Pool.Tenant, OzonAccount: h.f.req.Target.OzonAccount, MoyskladAccount: h.f.req.Pool.MoyskladAccount, ProductID: "manual-product", TargetKind: "variant", TargetID: "manual-variant", Evidence: domain.MappingOperatorAttested, SessionID: session, Lifetime: time.Millisecond}
	review, err := h.f.store.CreateMappingReview(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	w := h.post(confirmationPath(review.Review.ID), confirmationBody("expired-new", "synthetic"), h.cookie)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"REVIEW_EXPIRED"`) {
		t.Fatal("expired review confirmed")
	}
	h.assertRecords(t, "manual-product", 0, 0, 0, 0)
}

func TestMappingConfirmationExpiryDuringTransactionRollsBack(t *testing.T) {
	h := newConfirmationHarness(t)
	seed := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	ctx := context.Background()
	// Only this synthetic idempotency key is delayed, so the review can expire
	// after the first validation but before the transaction attempts to commit.
	_, err := h.f.store.Pool.Exec(ctx, `CREATE FUNCTION confirm_test_delay_outcome() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.idempotency_key='expiry-during-transaction' THEN PERFORM pg_sleep(2.5); END IF; RETURN NEW; END $$`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, e := h.f.store.Pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS confirm_test_delay_outcome()`); e != nil {
			t.Errorf("drop test function: %v", e)
		}
	})
	if _, err = h.f.store.Pool.Exec(ctx, `CREATE TRIGGER confirm_test_delay_outcome BEFORE INSERT ON product_mapping_confirmation_idempotency FOR EACH ROW EXECUTE FUNCTION confirm_test_delay_outcome()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, e := h.f.store.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS confirm_test_delay_outcome ON product_mapping_confirmation_idempotency`); e != nil {
			t.Errorf("drop test trigger: %v", e)
		}
	})
	input := domain.MappingReviewRequest{Tenant: h.f.req.Pool.Tenant, OzonAccount: h.f.req.Target.OzonAccount, MoyskladAccount: h.f.req.Pool.MoyskladAccount, ProductID: "manual-product", TargetKind: "variant", TargetID: "manual-variant", Evidence: domain.MappingOperatorAttested, SessionID: h.sessionID(t, seed.Review.ID), Lifetime: 2 * time.Second}
	review, err := h.f.store.CreateMappingReview(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	w := h.post(confirmationPath(review.Review.ID), confirmationBody("expiry-during-transaction", "synthetic"), h.cookie)
	if time.Since(started) < 2*time.Second {
		t.Fatal("test did not reach delayed transaction")
	}
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"REVIEW_EXPIRED"`) {
		t.Fatalf("expiry at commit not enforced: %d %s", w.Code, w.Body.String())
	}
	h.assertRecords(t, "manual-product", 0, 0, 0, 0)
	again := h.post(confirmationPath(review.Review.ID), confirmationBody("expiry-during-transaction", "synthetic"), h.cookie)
	if again.Code != 409 || !strings.Contains(again.Body.String(), `"REVIEW_EXPIRED"`) {
		t.Fatal("rolled-back request produced a replay")
	}
}

func TestMappingConfirmationTimeoutAndCancellationRollBack(t *testing.T) {
	t.Run("database timeout", func(t *testing.T) {
		h := newConfirmationHarness(t)
		review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
		ctx := context.Background()
		tx, err := h.f.store.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rollbackLeaseTest(t, tx)
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "ozon:"+h.f.req.Target.OzonAccount); err != nil {
			t.Fatal(err)
		}
		h.f.store.Timeout = 50 * time.Millisecond
		w := h.post(confirmationPath(review.Review.ID), confirmationBody("timeout-key", "synthetic"), h.cookie)
		if w.Code != 503 || !strings.Contains(w.Body.String(), `"PERSISTENCE_FAILURE"`) {
			t.Fatalf("timeout not handled: %d %s", w.Code, w.Body.String())
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		h.assertRecords(t, "manual-product", 0, 0, 0, 0)
		h.assertRecords(t, "product", 1, 1, 0, 0)
	})
	t.Run("request cancellation while waiting", func(t *testing.T) {
		h := newConfirmationHarness(t)
		review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
		background := context.Background()
		tx, err := h.f.store.Pool.Begin(background)
		if err != nil {
			t.Fatal(err)
		}
		defer rollbackLeaseTest(t, tx)
		if _, err = tx.Exec(background, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "ozon:"+h.f.req.Target.OzonAccount); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(background)
		defer cancel()
		request := domain.MappingConfirmationRequest{ReviewID: review.Review.ID, SessionID: h.sessionID(t, review.Review.ID), IdempotencyKey: "cancel-key", Acknowledged: true}
		done := make(chan error, 1)
		go func() { _, e := h.f.store.ConfirmMappingReview(ctx, request); done <- e }()
		select {
		case err := <-done:
			t.Fatalf("confirmation did not wait for the import lock: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
		cancel()
		select {
		case e := <-done:
			if e == nil {
				t.Fatal("canceled confirmation succeeded")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("canceled confirmation did not return")
		}
		if err = tx.Commit(background); err != nil {
			t.Fatal(err)
		}
		h.assertRecords(t, "manual-product", 0, 0, 0, 0)
		h.assertRecords(t, "product", 1, 1, 0, 0)
	})
}

func TestOperatorAttestedMappingDoesNotProveStockSafety(t *testing.T) {
	h := newConfirmationHarness(t)
	review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	var outboxBefore, decisionsBefore int
	if err := h.f.store.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM outbox_tasks),(SELECT count(*) FROM stock_decisions WHERE tenant_id=$1)`, h.f.req.Pool.Tenant).Scan(&outboxBefore, &decisionsBefore); err != nil {
		t.Fatal(err)
	}
	result := parseConfirmation(t, h.post(confirmationPath(review.Review.ID), confirmationBody("stock-safety-key", "synthetic"), h.cookie))
	var outboxAfter, decisionsAfter int
	if err := h.f.store.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM outbox_tasks),(SELECT count(*) FROM stock_decisions WHERE tenant_id=$1)`, h.f.req.Pool.Tenant).Scan(&outboxAfter, &decisionsAfter); err != nil {
		t.Fatal(err)
	}
	if outboxBefore != outboxAfter || decisionsBefore != decisionsAfter {
		t.Fatal("mapping scheduled publication or a stock decision")
	}
	h.f.req.Pool.ItemID = "manual-variant"
	h.f.req.Target.ProductID = "manual-product"
	decision := stockCalc(t, h.f)
	if decision.Status != domain.StockUnknown || decision.Quantity != nil || decision.Evidence.ProductMapping == nil || decision.Evidence.ProductMapping.ID != result.MappingID {
		t.Fatalf("operator attestation manufactured a ready decision: %+v", decision)
	}
	for _, reason := range []string{commitment.ProofUnit, commitment.ProofBaseline, commitment.ProofAttribution, commitment.ProofCompeting} {
		if !stockHasReason(decision, reason) {
			t.Fatalf("missing stock-safety reason %s", reason)
		}
	}
	_, err := h.f.store.Pool.Exec(context.Background(), `INSERT INTO stock_decisions(tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,policy_version,evaluated_at,expires_at,status,quantity,decision) SELECT tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,policy_version,evaluated_at,evaluated_at+interval '1 minute','READY_CONDITIONAL',1,decision FROM stock_decisions WHERE id=$1`, decision.ID)
	if err == nil {
		t.Fatal("database accepted READY_CONDITIONAL after attestation")
	}
	if err = h.f.store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_tasks`).Scan(&outboxAfter); err != nil || outboxAfter != outboxBefore {
		t.Fatal("stock inspection scheduled a publication task")
	}
}

func TestSharedAttestedSourcePoolRemainsBlocked(t *testing.T) {
	h := newConfirmationHarness(t)
	first := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	second := h.createReview(t, "second-offer", "variant", "manual-variant", domain.MappingOperatorAttested)
	parseConfirmation(t, h.post(confirmationPath(first.Review.ID), confirmationBody("pool-first", "synthetic"), h.cookie))
	parseConfirmation(t, h.post(confirmationPath(second.Review.ID), confirmationBody("pool-second", "synthetic"), h.cookie))
	h.f.req.Pool.ItemID = "manual-variant"
	h.f.req.Target.ProductID = "manual-product"
	decision := stockCalc(t, h.f)
	if decision.Status != domain.StockBlocked || decision.Quantity != nil || !decision.Evidence.SharedPool || !stockHasReason(decision, "SHARED_POOL_UNSUPPORTED") {
		t.Fatalf("shared inventory was not blocked: %+v", decision)
	}
}

func TestMappingConfirmationCanceledWhenOperatorLogsOut(t *testing.T) {
	h := newConfirmationHarness(t)
	review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	ctx := context.Background()
	tx, err := h.f.store.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackLeaseTest(t, tx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "ozon:"+h.f.req.Target.OzonAccount); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- h.post(confirmationPath(review.Review.ID), confirmationBody("logout-key", "synthetic"), h.cookie)
	}()
	select {
	case <-done:
		t.Fatal("confirmation did not wait for import lock")
	case <-time.After(50 * time.Millisecond):
	}
	logout := h.call("DELETE", "/api/session", "", confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie)
	if logout.Code != 204 {
		t.Fatalf("logout failed: %d", logout.Code)
	}
	select {
	case w := <-done:
		if w.Code != 503 || !strings.Contains(w.Body.String(), `"PERSISTENCE_FAILURE"`) {
			t.Fatalf("in-flight request was not canceled: %d %s", w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("logout did not cancel pending confirmation")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	h.assertRecords(t, "manual-product", 0, 0, 0, 0)
	if w := h.post(confirmationPath(review.Review.ID), confirmationBody("logout-key", "synthetic"), h.cookie); w.Code != 401 {
		t.Fatal("logged-out session remained authorized")
	}
}

func TestMappingHistoryRemainsInspectableAfterSessionLoss(t *testing.T) {
	h := newConfirmationHarness(t)
	review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	body := confirmationBody("session-loss-key", "synthetic explanation")
	first := parseConfirmation(t, h.post(confirmationPath(review.Review.ID), body, h.cookie))
	auditID := h.sessionID(t, review.Review.ID)
	if w := h.call("DELETE", "/api/session", "", confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie); w.Code != 204 {
		t.Fatal("logout failed")
	}
	replacement := h.login(t)
	if w := h.post(confirmationPath(review.Review.ID), body, replacement); w.Code != 404 || !strings.Contains(w.Body.String(), `"REVIEW_NOT_FOUND"`) {
		t.Fatal("new session replayed old session request")
	}
	q := url.Values{"tenant": {h.f.req.Pool.Tenant}, "ozon_account": {h.f.req.Target.OzonAccount}, "moysklad_account": {h.f.req.Pool.MoyskladAccount}, "product": {"manual-product"}}
	w := h.call("GET", "/api/product-mapping-history?"+q.Encode(), "", "", "127.0.0.1:8080", "127.0.0.1:1234", replacement)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"review_id":"`+review.Review.ID+`"`) || !strings.Contains(w.Body.String(), `"evidence_class":"OPERATOR_ATTESTED"`) || !strings.Contains(w.Body.String(), `"principal_type":"LOCAL_OPERATOR_SESSION"`) || !strings.Contains(w.Body.String(), `"explanation":"synthetic explanation"`) {
		t.Fatalf("committed mapping history unavailable: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), auditID) || strings.Contains(w.Body.String(), h.cookie.Value) || strings.Contains(w.Body.String(), "session-loss-key") {
		t.Fatal("history disclosed private session or idempotency data")
	}
	if first.MappingID <= 0 {
		t.Fatal("missing mapping")
	}
	h.assertRecords(t, "manual-product", 1, 1, 1, 1)
}

func TestMappingConfirmationSerializesWithLegacyCLIPath(t *testing.T) {
	h := newConfirmationHarness(t)
	review := h.createReview(t, "candidate-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	scope := postgres.MatchScope{Tenant: h.f.req.Pool.Tenant, OzonAccount: h.f.req.Target.OzonAccount, MoyskladAccount: h.f.req.Pool.MoyskladAccount}
	start := make(chan struct{})
	apiDone := make(chan *httptest.ResponseRecorder, 1)
	legacyDone := make(chan error, 1)
	go func() {
		<-start
		apiDone <- h.post(confirmationPath(review.Review.ID), confirmationBody("legacy-race", "synthetic"), h.cookie)
	}()
	go func() {
		<-start
		_, err := h.f.store.ConfirmMatch(context.Background(), scope, "candidate-product", "product", "candidate-item", "synthetic legacy operator")
		legacyDone <- err
	}()
	close(start)
	api, legacyErr := <-apiDone, <-legacyDone
	if api.Code == 200 {
		if legacyErr == nil {
			t.Fatal("conflicting legacy confirmation also succeeded")
		}
		h.assertRecords(t, "candidate-product", 1, 1, 1, 1)
	} else {
		if api.Code != 409 || !strings.Contains(api.Body.String(), `"REVIEW_STALE"`) || legacyErr != nil {
			t.Fatalf("unexpected competing outcomes: api=%d legacy=%v", api.Code, legacyErr)
		}
		h.assertRecords(t, "candidate-product", 1, 1, 0, 0)
	}
}

func TestMappingConfirmationRejectsOfferIdentityConflict(t *testing.T) {
	h := newConfirmationHarness(t)
	next := h.f.ozon
	next.Products[0] = domain.OzonProduct{ProductID: "moved-product", OfferID: "offer"}
	next.Stocks = next.Stocks[1:]
	next.StartedAt = time.Now()
	next.FinishedAt = time.Now().Add(time.Second)
	if _, err := h.f.store.SaveOzonImport(context.Background(), next, "confirmation-offer-conflict"); err != nil {
		t.Fatal(err)
	}
	review := h.createReview(t, "moved-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	if !review.MappingConflict {
		t.Fatal("existing offer conflict not identified")
	}
	w := h.post(confirmationPath(review.Review.ID), confirmationBody("offer-conflict-key", "synthetic"), h.cookie)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"MAPPING_CONFLICT"`) {
		t.Fatalf("offer conflict accepted: %d %s", w.Code, w.Body.String())
	}
	h.assertRecords(t, "moved-product", 0, 0, 0, 0)
	h.assertRecords(t, "product", 1, 1, 0, 0)
}

func TestMappingConfirmationRequiresJSONContentType(t *testing.T) {
	h := newConfirmationHarness(t)
	review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	r := httptest.NewRequest("POST", "http://127.0.0.1:8080"+confirmationPath(review.Review.ID), strings.NewReader(confirmationBody("content-type-key", "synthetic")))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Origin", confirmationTestOrigin)
	r.Header.Set("Content-Type", "text/plain")
	r.AddCookie(h.cookie)
	w := httptest.NewRecorder()
	h.h.ServeHTTP(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"INVALID_REQUEST"`) {
		t.Fatal("unexpected content type accepted")
	}
	h.assertRecords(t, "manual-product", 0, 0, 0, 0)
}

func TestMappingConfirmationRejectsChangedMappingFingerprint(t *testing.T) {
	h := newConfirmationHarness(t)
	review := h.createReview(t, "product", "variant", "item", domain.MappingOperatorAttested)
	if _, err := h.f.store.Pool.Exec(context.Background(), `UPDATE product_mappings SET provenance='changed synthetic provenance' WHERE tenant_id=$1 AND ozon_account=$2 AND ozon_product_id='product'`, h.f.req.Pool.Tenant, h.f.req.Target.OzonAccount); err != nil {
		t.Fatal(err)
	}
	w := h.post(confirmationPath(review.Review.ID), confirmationBody("mapping-fingerprint", "synthetic"), h.cookie)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"REVIEW_STALE"`) {
		t.Fatal("changed mapping fingerprint accepted")
	}
	h.assertRecords(t, "product", 1, 1, 0, 0)
}

func TestMappingConfirmationRequiresObservedOfferIdentity(t *testing.T) {
	h := newConfirmationHarness(t)
	next := h.f.ozon
	next.Products = append(next.Products, domain.OzonProduct{ProductID: "missing-offer", OfferID: ""})
	next.StartedAt = time.Now()
	next.FinishedAt = time.Now().Add(time.Second)
	if _, err := h.f.store.SaveOzonImport(context.Background(), next, "confirmation-missing-offer"); err != nil {
		t.Fatal(err)
	}
	review := h.createReview(t, "missing-offer", "variant", "manual-variant", domain.MappingOperatorAttested)
	w := h.post(confirmationPath(review.Review.ID), confirmationBody("missing-offer-key", "synthetic"), h.cookie)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"REVIEW_STALE"`) {
		t.Fatalf("missing offer identity accepted: %d %s", w.Code, w.Body.String())
	}
	h.assertRecords(t, "missing-offer", 0, 0, 0, 0)
}

func TestMappingConfirmationRejectsChangedImportFingerprint(t *testing.T) {
	for _, source := range []string{"ozon", "moysklad"} {
		t.Run(source, func(t *testing.T) {
			h := newConfirmationHarness(t)
			review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
			importID := review.Review.OzonImportID
			if source == "moysklad" {
				importID = review.Review.MoyskladImportID
			}
			if _, err := h.f.store.Pool.Exec(context.Background(), `UPDATE source_imports SET fingerprint='synthetic-fingerprint-changed' WHERE id=$1`, importID); err != nil {
				t.Fatal(err)
			}
			w := h.post(confirmationPath(review.Review.ID), confirmationBody("fingerprint-change", "synthetic"), h.cookie)
			if w.Code != 409 || !strings.Contains(w.Body.String(), `"REVIEW_STALE"`) {
				t.Fatalf("changed import fingerprint accepted: %d %s", w.Code, w.Body.String())
			}
			h.assertRecords(t, "manual-product", 0, 0, 0, 0)
		})
	}
}

func TestLegacyReviewWithoutImportFingerprintsMustBeRecreated(t *testing.T) {
	h := newConfirmationHarness(t)
	seed := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	// Reproduce a pre-migration review without modifying the existing immutable row.
	prefix := "0"
	if strings.HasPrefix(seed.Review.ID, "0") {
		prefix = "1"
	}
	legacyID := prefix + seed.Review.ID[1:]
	_, err := h.f.store.Pool.Exec(context.Background(), `INSERT INTO product_mapping_reviews(id,tenant_id,ozon_account,moysklad_account,ozon_product_id,target_kind,target_id,ozon_import_id,moysklad_import_id,existing_mapping_id,existing_mapping_version,observation_hash,mapping_hash,evidence_class,principal_type,session_audit_id,created_at,expires_at) SELECT $2,tenant_id,ozon_account,moysklad_account,ozon_product_id,target_kind,target_id,ozon_import_id,moysklad_import_id,existing_mapping_id,existing_mapping_version,observation_hash,mapping_hash,evidence_class,principal_type,session_audit_id,created_at,expires_at FROM product_mapping_reviews WHERE id=$1`, seed.Review.ID, legacyID)
	if err != nil {
		t.Fatal(err)
	}
	inspected := h.call("GET", "/api/product-mapping-reviews/"+legacyID, "", "", "127.0.0.1:8080", "127.0.0.1:1234", h.cookie)
	if inspected.Code != 200 || !strings.Contains(inspected.Body.String(), `"Status":"STALE_SOURCE"`) {
		t.Fatal("legacy review was not marked stale")
	}
	w := h.post(confirmationPath(legacyID), confirmationBody("legacy-review-key", "synthetic"), h.cookie)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"REVIEW_STALE"`) {
		t.Fatal("review without fingerprint snapshots confirmed")
	}
	h.assertRecords(t, "manual-product", 0, 0, 0, 0)
	// A fresh, fully revision-bound review can still be confirmed explicitly.
	parseConfirmation(t, h.post(confirmationPath(seed.Review.ID), confirmationBody("fresh-review-key", "synthetic"), h.cookie))
	h.assertRecords(t, "manual-product", 1, 1, 1, 1)
}

func TestReviewBoundCommitRejectsMismatchedAuditOrActiveMapping(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		changeMapping, changeTime bool
	}{{"active mapping differs", true, false}, {"audit timestamp differs", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			h := newConfirmationHarness(t)
			review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
			session := h.sessionID(t, review.Review.ID)
			ctx := context.Background()
			tx, err := h.f.store.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rollbackLeaseTest(t, tx)
			var id int64
			err = tx.QueryRow(ctx, `INSERT INTO product_mappings(tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,version,provenance) VALUES($1,$2,'manual-product','seller-offer',$3,'variant','manual-variant',1,'LOCAL_OPERATOR_SESSION') RETURNING id`, h.f.req.Pool.Tenant, h.f.req.Target.OzonAccount, h.f.req.Pool.MoyskladAccount).Scan(&id)
			if err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, `INSERT INTO product_mapping_history(mapping_id,version,tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,provenance,confirmed_at,ozon_import_id,moysklad_import_id,confirmation_review_id) SELECT id,version,tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,provenance,confirmed_at,$2,$3,$4 FROM product_mappings WHERE id=$1`, id, review.Review.OzonImportID, review.Review.MoyskladImportID, review.Review.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.changeMapping {
				if _, err = tx.Exec(ctx, `UPDATE product_mappings SET target_id='other-item' WHERE id=$1`, id); err != nil {
					t.Fatal(err)
				}
			}
			shift := time.Duration(0)
			if tc.changeTime {
				shift = time.Second
			}
			_, err = tx.Exec(ctx, `INSERT INTO product_mapping_confirmation_audits(mapping_id,mapping_version,review_id,tenant_id,ozon_account,moysklad_account,ozon_product_id,ozon_offer_id,target_kind,target_id,ozon_import_id,moysklad_import_id,evidence_class,principal_type,session_audit_id,explanation,confirmed_at) SELECT h.mapping_id,h.version,h.confirmation_review_id,h.tenant_id,h.ozon_account,h.moysklad_account,h.ozon_product_id,h.ozon_offer_id,h.target_kind,h.target_id,h.ozon_import_id,h.moysklad_import_id,'OPERATOR_ATTESTED','LOCAL_OPERATOR_SESSION',$2,'synthetic',h.confirmed_at+$3::interval FROM product_mapping_history h WHERE h.mapping_id=$1`, id, session, shift.String())
			if err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, `INSERT INTO product_mapping_confirmation_idempotency(review_id,session_audit_id,idempotency_key,request_hash,mapping_id,mapping_version) VALUES($1,$2,'integrity-test',repeat('a',64),$3,1)`, review.Review.ID, session, id)
			if err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err == nil {
				t.Fatal("inconsistent mapping or audit committed")
			}
			h.assertRecords(t, "manual-product", 0, 0, 0, 0)
		})
	}
}

type disconnectedConfirmationWriter struct {
	header    http.Header
	attempted bool
}

func (w *disconnectedConfirmationWriter) Header() http.Header { return w.header }
func (w *disconnectedConfirmationWriter) WriteHeader(int)     {}
func (w *disconnectedConfirmationWriter) Write([]byte) (int, error) {
	w.attempted = true
	return 0, io.ErrClosedPipe
}

func TestMappingConfirmationRecoversFromDisconnectedResponse(t *testing.T) {
	h := newConfirmationHarness(t)
	review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	body := confirmationBody("disconnected-response", "synthetic")
	req := httptest.NewRequest("POST", "http://127.0.0.1:8080"+confirmationPath(review.Review.ID), strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Origin", confirmationTestOrigin)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(h.cookie)
	writer := &disconnectedConfirmationWriter{header: make(http.Header)}
	h.h.ServeHTTP(writer, req)
	if !writer.attempted {
		t.Fatal("test did not interrupt the response")
	}
	h.assertRecords(t, "manual-product", 1, 1, 1, 1)
	replay := parseConfirmation(t, h.post(confirmationPath(review.Review.ID), body, h.cookie))
	if !replay.IdempotentReplay || replay.MappingID <= 0 {
		t.Fatal("committed mapping was not recovered")
	}
	h.assertRecords(t, "manual-product", 1, 1, 1, 1)
}

func TestProductAttestationDoesNotEstablishWarehouseMapping(t *testing.T) {
	h := newConfirmationHarness(t)
	review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	parseConfirmation(t, h.post(confirmationPath(review.Review.ID), confirmationBody("no-warehouse-proof", "synthetic"), h.cookie))
	h.f.req.Pool.ItemID = "manual-variant"
	h.f.req.Pool.WarehouseID = "unmapped-source-warehouse"
	h.f.req.Target.ProductID = "manual-product"
	h.f.req.Target.WarehouseID = "unmapped-ozon-warehouse"
	decision := stockCalc(t, h.f)
	if decision.Quantity != nil || decision.Status == domain.StockReady || decision.Evidence.WarehouseMapping != nil || !stockHasReason(decision, commitment.ProofWarehouseMapping) || !stockHasReason(decision, commitment.ProofUnit) {
		t.Fatalf("product attestation incorrectly established warehouse or unit proof: %+v", decision)
	}
}

func TestMappingConfirmationTimestampFollowsLockWait(t *testing.T) {
	h := newConfirmationHarness(t)
	review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	ctx := context.Background()
	tx, err := h.f.store.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackLeaseTest(t, tx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "ozon:"+h.f.req.Target.OzonAccount); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- h.post(confirmationPath(review.Review.ID), confirmationBody("timestamp-after-lock", "synthetic"), h.cookie)
	}()
	select {
	case <-done:
		t.Fatal("confirmation did not wait for lock")
	case <-time.After(100 * time.Millisecond):
	}
	// PostgreSQL and the test process use the same local test host clock; allow
	// a small scheduling margin while excluding the transaction start time.
	notBefore := time.Now().Add(-5 * time.Millisecond)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		result := parseConfirmation(t, w)
		if result.ConfirmedAt.Before(notBefore) {
			t.Fatal("confirmation timestamp predates lock release")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("confirmation did not resume")
	}
}

func TestMappingConfirmationConcurrentDifferentKeysSameReview(t *testing.T) {
	h := newConfirmationHarness(t)
	review := h.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
	start := make(chan struct{})
	done := make(chan *httptest.ResponseRecorder, 2)
	for _, key := range []string{"different-key-a", "different-key-b"} {
		go func(key string) {
			<-start
			done <- h.post(confirmationPath(review.Review.ID), confirmationBody(key, "synthetic"), h.cookie)
		}(key)
	}
	close(start)
	a, b := <-done, <-done
	if (a.Code != http.StatusOK || b.Code != http.StatusConflict) && (a.Code != http.StatusConflict || b.Code != http.StatusOK) {
		t.Fatalf("different keys produced unexpected outcomes: %d/%d", a.Code, b.Code)
	}
	conflict := a
	if b.Code == 409 {
		conflict = b
	}
	if !strings.Contains(conflict.Body.String(), `"REVIEW_STALE"`) {
		t.Fatal("different key did not revalidate changed mapping")
	}
	h.assertRecords(t, "manual-product", 1, 1, 1, 1)
}

func TestConfirmationAuditRejectsCrossTenantAndCrossAccountReferences(t *testing.T) {
	for _, mode := range []string{"tenant", "account"} {
		t.Run(mode, func(t *testing.T) {
			source := newConfirmationHarness(t)
			review := source.createReview(t, "manual-product", "variant", "manual-variant", domain.MappingOperatorAttested)
			session := source.sessionID(t, review.Review.ID)
			owner := source
			tenant, moy := source.f.req.Pool.Tenant, source.f.req.Pool.MoyskladAccount
			var ozon string
			if mode == "tenant" {
				owner = newConfirmationHarness(t)
				tenant, ozon, moy = owner.f.req.Pool.Tenant, owner.f.req.Target.OzonAccount, owner.f.req.Pool.MoyskladAccount
			} else {
				ozon = source.f.req.Target.OzonAccount + "-other"
				if err := source.f.store.RegisterStockConnection(context.Background(), tenant, "ozon", ozon, "synthetic registration", true); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			tx, err := owner.f.store.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var mappingID int64
			err = tx.QueryRow(ctx, `INSERT INTO product_mappings(tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,version,provenance) VALUES($1,$2,'manual-product','seller-offer',$3,'variant','manual-variant',1,'LOCAL_OPERATOR_SESSION') RETURNING id`, tenant, ozon, moy).Scan(&mappingID)
			if err != nil {
				_ = tx.Rollback(ctx)
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, `INSERT INTO product_mapping_history(mapping_id,version,tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,provenance,confirmed_at,ozon_import_id,moysklad_import_id,confirmation_review_id) SELECT id,version,tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,provenance,confirmed_at,$2,$3,$4 FROM product_mappings WHERE id=$1`, mappingID, review.Review.OzonImportID, review.Review.MoyskladImportID, review.Review.ID)
			if err != nil {
				_ = tx.Rollback(ctx)
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, `INSERT INTO product_mapping_confirmation_audits(mapping_id,mapping_version,review_id,tenant_id,ozon_account,moysklad_account,ozon_product_id,ozon_offer_id,target_kind,target_id,ozon_import_id,moysklad_import_id,evidence_class,principal_type,session_audit_id,explanation,confirmed_at) SELECT h.mapping_id,h.version,h.confirmation_review_id,h.tenant_id,h.ozon_account,h.moysklad_account,h.ozon_product_id,h.ozon_offer_id,h.target_kind,h.target_id,h.ozon_import_id,h.moysklad_import_id,'OPERATOR_ATTESTED','LOCAL_OPERATOR_SESSION',$2,'synthetic',h.confirmed_at FROM product_mapping_history h WHERE h.mapping_id=$1`, mappingID, session)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "23503" {
				_ = tx.Rollback(ctx)
				t.Fatalf("cross-scope audit was not rejected by a foreign key: %v", err)
			}
			if err = tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			var count int
			if err = owner.f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM product_mappings WHERE tenant_id=$1 AND ozon_account=$2 AND ozon_product_id='manual-product'`, tenant, ozon).Scan(&count); err != nil || count != 0 {
				t.Fatal("cross-scope mapping was partially persisted")
			}
		})
	}
}
