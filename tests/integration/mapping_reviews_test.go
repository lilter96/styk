//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
)

func TestDurableReviewScopedSessionAndSourceRevisions(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	secret := strings.Repeat("s", 32)
	h, err := console.Handler(f.store, secret, "http://127.0.0.1:5173")
	if err != nil {
		t.Fatal(err)
	}
	req := func(method, path, body, origin string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	login := func() *http.Cookie {
		w := req("POST", "/api/session", fmt.Sprintf(`{"secret":%q}`, secret), "http://127.0.0.1:5173", nil)
		if w.Code != 204 {
			t.Fatal("login failed")
		}
		return w.Result().Cookies()[0]
	}
	cookie := login()
	body := fmt.Sprintf(`{"tenant":%q,"ozon_account":%q,"moysklad_account":%q,"product_id":"product","target_kind":"variant","target_id":"item","evidence_class":"OPERATOR_ATTESTED"}`, f.req.Pool.Tenant, f.req.Target.OzonAccount, f.req.Pool.MoyskladAccount)
	path := "/api/product-mapping-reviews"
	if w := req("POST", path, body, "http://127.0.0.1:5173", nil); w.Code != 401 {
		t.Fatal("unauthenticated creation")
	}
	if w := req("POST", path, body, "http://untrusted.invalid", cookie); w.Code != 403 {
		t.Fatal("cross origin accepted")
	}
	if w := req("POST", path, strings.Replace(body, "OPERATOR_ATTESTED", "INDEPENDENTLY_VERIFIED", 1), "http://127.0.0.1:5173", cookie); w.Code != 400 {
		t.Fatal("unverified evidence accepted")
	}
	if w := req("POST", path, strings.TrimSuffix(body, "}")+`,"session_id":"fake"}`, "http://127.0.0.1:5173", cookie); w.Code != 400 {
		t.Fatal("fake principal accepted")
	}
	var beforeM, beforeH, beforeO int
	for _, q := range []struct {
		sql string
		v   *int
	}{{`SELECT count(*) FROM product_mappings`, &beforeM}, {`SELECT count(*) FROM product_mapping_history`, &beforeH}, {`SELECT count(*) FROM outbox_tasks`, &beforeO}} {
		if err = f.store.Pool.QueryRow(ctx, q.sql).Scan(q.v); err != nil {
			t.Fatal(err)
		}
	}
	w := req("POST", path, body, "http://127.0.0.1:5173", cookie)
	if w.Code != 201 {
		t.Fatalf("creation status %d: %s", w.Code, w.Body.String())
	}
	var created domain.MappingReviewResult
	if err = json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Status != "CURRENT" || created.Review.Target.Kind != "variant" || created.Review.EvidenceClass != domain.MappingOperatorAttested {
		t.Fatal("wrong review")
	}
	var auditID string
	if err = f.store.Pool.QueryRow(ctx, `SELECT session_audit_id FROM product_mapping_reviews WHERE id=$1`, created.Review.ID).Scan(&auditID); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), cookie.Value) || strings.Contains(w.Body.String(), auditID) {
		t.Fatal("credential or private audit identifier exposed")
	}
	if auditID == cookie.Value || strings.Contains(auditID, secret) || len(auditID) < 20 {
		t.Fatal("session audit identity is not independent")
	}
	second := req("POST", path, body, "http://127.0.0.1:5173", cookie)
	var duplicate domain.MappingReviewResult
	if second.Code != 201 || json.Unmarshal(second.Body.Bytes(), &duplicate) != nil {
		t.Fatal("second review failed")
	}
	var secondAudit string
	if err = f.store.Pool.QueryRow(ctx, `SELECT session_audit_id FROM product_mapping_reviews WHERE id=$1`, duplicate.Review.ID).Scan(&secondAudit); err != nil || secondAudit != auditID {
		t.Fatal("session audit identity changed")
	}
	if w = req("GET", path+"/"+created.Review.ID, "", "", login()); w.Code != 404 {
		t.Fatal("another session read review")
	}
	w = req("GET", path+"/"+created.Review.ID, "", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"Status":"CURRENT"`) {
		t.Fatal("current review unavailable")
	}
	if _, err = f.store.Pool.Exec(ctx, `UPDATE product_mapping_reviews SET evidence_class='OPERATOR_ATTESTED' WHERE id=$1`, created.Review.ID); err == nil {
		t.Fatal("review history mutable")
	}
	var afterM, afterH, afterO int
	for _, q := range []struct {
		sql string
		v   *int
	}{{`SELECT count(*) FROM product_mappings`, &afterM}, {`SELECT count(*) FROM product_mapping_history`, &afterH}, {`SELECT count(*) FROM outbox_tasks`, &afterO}} {
		if err = f.store.Pool.QueryRow(ctx, q.sql).Scan(q.v); err != nil {
			t.Fatal(err)
		}
	}
	if beforeM != afterM || beforeH != afterH || beforeO != afterO {
		t.Fatal("review caused business side effects")
	}
	f.ozon.Products[0].OfferID = "changed"
	f.ozon.Stocks = nil
	f.ozon.StartedAt = time.Now()
	f.ozon.FinishedAt = time.Now().Add(time.Second)
	if _, err = f.store.SaveOzonImport(ctx, f.ozon, "review-changed"); err != nil {
		t.Fatal(err)
	}
	w = req("GET", path+"/"+created.Review.ID, "", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"Status":"STALE_SOURCE"`) {
		t.Fatal("new source revision was not detected")
	}
}

func TestReviewEvidenceExpiryAndMappingChanges(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	base := domain.MappingReviewRequest{Tenant: f.req.Pool.Tenant, OzonAccount: f.req.Target.OzonAccount, MoyskladAccount: f.req.Pool.MoyskladAccount, ProductID: "product", TargetKind: "variant", TargetID: "item", Evidence: domain.MappingDeterministicCandidate, SessionID: "synthetic-session", Lifetime: time.Minute}
	review, err := f.store.CreateMappingReview(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if review.PrincipalType != "LOCAL_OPERATOR_SESSION" {
		t.Fatal("incorrect principal")
	}
	bad := base
	bad.TargetID = "missing"
	if _, err = f.store.CreateMappingReview(ctx, bad); err != postgres.ErrReviewSource {
		t.Fatal("missing target accepted")
	}
	bad = base
	bad.ProductID = "missing"
	if _, err = f.store.CreateMappingReview(ctx, bad); err != postgres.ErrReviewSource {
		t.Fatal("missing product accepted")
	}
	bad = base
	bad.Tenant += "-other"
	if _, err = f.store.CreateMappingReview(ctx, bad); err != postgres.ErrReviewScope {
		t.Fatal("cross tenant accepted")
	}
	bad = base
	bad.OzonAccount += "-other"
	if _, err = f.store.CreateMappingReview(ctx, bad); err != postgres.ErrReviewScope {
		t.Fatal("cross account accepted")
	}
	bad = base
	bad.Evidence = domain.MappingIndependentlyVerified
	if _, err = f.store.CreateMappingReview(ctx, bad); err != postgres.ErrReviewEvidence {
		t.Fatal("independent evidence accepted")
	}
	bad = base
	bad.Lifetime = time.Microsecond
	short, err := f.store.CreateMappingReview(ctx, bad)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	expired, err := f.store.InspectMappingReview(ctx, short.Review.ID, base.SessionID)
	if err != nil || expired.Status != "EXPIRED" {
		t.Fatal("expiration missing")
	}
	if _, err = f.store.Pool.Exec(ctx, `UPDATE product_mappings SET version=version+1 WHERE tenant_id=$1 AND ozon_account=$2 AND ozon_product_id=$3`, base.Tenant, base.OzonAccount, base.ProductID); err != nil {
		t.Fatal(err)
	}
	stale, err := f.store.InspectMappingReview(ctx, review.Review.ID, base.SessionID)
	if err != nil || stale.Status != "STALE_MAPPING" {
		t.Fatalf("mapping change not detected: %s %v", stale.Status, err)
	}
}

func TestReviewManualSelectionAndMoyskladRevision(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	f.source.Entities = append(f.source.Entities, domain.ExternalEntity{Kind: "product", ID: "manual", Article: "different", Code: "other"})
	f.source.StartedAt = now
	f.source.FinishedAt = now.Add(time.Second)
	if _, err := f.store.SaveInventoryImport(ctx, f.source, "manual-review-source"); err != nil {
		t.Fatal(err)
	}
	in := domain.MappingReviewRequest{Tenant: f.req.Pool.Tenant, OzonAccount: f.req.Target.OzonAccount, MoyskladAccount: f.req.Pool.MoyskladAccount, ProductID: "product", TargetKind: "product", TargetID: "manual", Evidence: domain.MappingOperatorAttested, SessionID: "synthetic-session", Lifetime: time.Minute}
	r, err := f.store.CreateMappingReview(ctx, in)
	if err != nil || !r.MappingConflict {
		t.Fatalf("manual review or conflict: %v", err)
	}
	in.Evidence = domain.MappingDeterministicCandidate
	if _, err = f.store.CreateMappingReview(ctx, in); err != postgres.ErrReviewEvidence {
		t.Fatal("fabricated candidate accepted")
	}
	f.source.Entities[2].Code = "new-code"
	f.source.StartedAt = time.Now()
	f.source.FinishedAt = time.Now().Add(time.Second)
	if _, err = f.store.SaveInventoryImport(ctx, f.source, "manual-review-changed"); err != nil {
		t.Fatal(err)
	}
	changed, err := f.store.InspectMappingReview(ctx, r.Review.ID, in.SessionID)
	if err != nil || changed.Status != "STALE_SOURCE" {
		t.Fatal("changed MoySklad import not detected")
	}
}

func TestReviewCreationCoordinatesWithImportLock(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	tx, err := f.store.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "ozon:"+f.req.Target.OzonAccount); err != nil {
		t.Fatal(err)
	}
	input := domain.MappingReviewRequest{Tenant: f.req.Pool.Tenant, OzonAccount: f.req.Target.OzonAccount, MoyskladAccount: f.req.Pool.MoyskladAccount, ProductID: "product", TargetKind: "variant", TargetID: "item", Evidence: domain.MappingOperatorAttested, SessionID: "synthetic-session", Lifetime: time.Minute}
	type outcome struct {
		r   domain.MappingReviewResult
		err error
	}
	done := make(chan outcome, 1)
	go func() { r, err := f.store.CreateMappingReview(ctx, input); done <- outcome{r, err} }()
	select {
	case <-done:
		t.Fatal("review bypassed importer lock")
	case <-time.After(50 * time.Millisecond):
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO source_imports(source,account_id,fingerprint,started_at,finished_at,report_scope) VALUES('ozon',$1,'review-concurrency',now(),now(),'synthetic') RETURNING id`, f.req.Target.OzonAccount).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ozon_import_batches(import_id,traversal_status) VALUES($1,'completed')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ozon_product_observations(import_id,product_id,offer_id) VALUES($1,'product','offer')`, id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-done:
		if out.err != nil {
			t.Fatal(out.err)
		}
		if out.r.Review.OzonImportID != id {
			t.Fatal("review captured pre-commit import")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("review did not resume")
	}
}

func TestReviewMissingImportAndInPlaceObservationChanges(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	scope := postgres.MatchScope{Tenant: f.req.Pool.Tenant, OzonAccount: f.req.Target.OzonAccount, MoyskladAccount: f.req.Pool.MoyskladAccount}
	missingAccount := f.req.Pool.MoyskladAccount + "-empty"
	if err := f.store.RegisterStockConnection(ctx, scope.Tenant, "moysklad", missingAccount, "synthetic", true); err != nil {
		t.Fatal(err)
	}
	in := domain.MappingReviewRequest{Tenant: scope.Tenant, OzonAccount: scope.OzonAccount, MoyskladAccount: missingAccount, ProductID: "product", TargetKind: "variant", TargetID: "item", Evidence: domain.MappingOperatorAttested, SessionID: "synthetic", Lifetime: time.Minute}
	if _, err := f.store.CreateMappingReview(ctx, in); err != postgres.ErrReviewSource {
		t.Fatal("missing import accepted")
	}
	in.MoyskladAccount = scope.MoyskladAccount
	result, err := f.store.CreateMappingReview(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Pool.Exec(ctx, `UPDATE source_entities SET article='synthetic-change' WHERE import_id=$1 AND kind='variant' AND external_id='item'`, result.Review.MoyskladImportID); err != nil {
		t.Fatal(err)
	}
	updated, err := f.store.InspectMappingReview(ctx, result.Review.ID, in.SessionID)
	if err != nil || updated.Status != "STALE_SOURCE" {
		t.Fatal("in-place observation change not detected")
	}
}

func TestReviewSourceIdentityAbsentFromLaterCompletedImport(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	in := domain.MappingReviewRequest{Tenant: f.req.Pool.Tenant, OzonAccount: f.req.Target.OzonAccount, MoyskladAccount: f.req.Pool.MoyskladAccount, ProductID: "product", TargetKind: "variant", TargetID: "item", Evidence: domain.MappingOperatorAttested, SessionID: "synthetic-session", Lifetime: time.Minute}
	review, err := f.store.CreateMappingReview(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	next := f.ozon
	next.Products = nil
	next.Stocks = nil
	next.StartedAt = time.Now()
	next.FinishedAt = time.Now().Add(time.Second)
	if _, err = f.store.SaveOzonImport(ctx, next, "review-missing-product"); err != nil {
		t.Fatal(err)
	}
	result, err := f.store.InspectMappingReview(ctx, review.Review.ID, in.SessionID)
	if err != nil || result.Status != "STALE_SOURCE" {
		t.Fatal("missing identity not reported as stale")
	}
}
