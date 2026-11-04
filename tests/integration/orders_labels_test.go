//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/orderprivacy"
	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersLabelStorageIsAtomicOwnedAndSessionBound(t *testing.T) {
	f := newLabelStoreFixture(t)
	w, receipt := labelStoreWork(t, f)
	vault, err := credentials.New(bytes.Repeat([]byte{31}, 32))
	if err != nil {
		t.Fatal(err)
	}
	protector := orderprivacy.LabelProtector{Vault: vault}
	pdf := []byte("%PDF-1.7\nSYNTHETIC_RECEIVER\n%%EOF")
	a, err := protector.SealOrderLabel(w, receipt.TaskIDs[0], pdf, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	receipt.State = orders.StateReconciled
	receipt.LabelTask = &orders.LabelTask{ID: "99", Code: "completed", CountsKnown: true, PostingsCount: 1, PrintedPostingsCount: 1}
	receipt.Artifact = &orders.OrderArtifact{Available: true, DownloadURL: orders.LabelArtifactURL(w.ID), LabelType: "big_label", ExpiresAt: a.ExpiresAt}
	c := orders.Completion{WorkID: w.ID, Scope: w.Scope, ClaimToken: w.ClaimToken, ExpectedState: w.State, State: orders.StateReconciled, Receipt: receipt}
	if err = f.db.CompleteOrderWork(f.ctx, c); !errors.Is(err, orders.ErrStale) {
		t.Fatalf("missing PDF reconciled: %v", err)
	}
	c.LabelArtifact = &a
	a.Scope.OzonAccount = "other"
	if err = f.db.CompleteOrderWork(f.ctx, c); !errors.Is(err, orders.ErrStale) {
		t.Fatalf("wrong account label persisted: %v", err)
	}
	a.Scope = w.Scope
	if err = f.db.CompleteOrderWork(f.ctx, c); !errors.Is(err, orders.ErrStale) {
		t.Fatal("configured retention exceeded")
	}
	a.ExpiresAt = a.CreatedAt.Add(time.Hour)
	c.Receipt.Artifact.ExpiresAt = a.ExpiresAt
	if err = f.db.CompleteOrderWork(f.ctx, c); err != nil {
		t.Fatal(err)
	}
	assertStoredLabel(t, f, w.ID, protector, pdf)
	assertLabelRollbackRejected(t, f)
	assertLabelHTTPIsolation(t, f, w.ID, pdf)
	assertLabelStoreSessionIsolation(t, f, w.ID)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_label_artifacts`, 1)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_receipts`, 2)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_receipts WHERE receipt::text LIKE '%https://%'`, 0)
}

func newLabelStoreFixture(t *testing.T) ordersStoreFixture {
	t.Helper()
	f := newOrdersStoreFixture(t)
	config := f.config
	config.PIIEnabled = true
	config.PIIRetentionSeconds = 3600
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: f.session.AuditID, IdempotencyKey: "label-retention"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func assertLabelStoreSessionIsolation(t *testing.T, f ordersStoreFixture, workID string) {
	t.Helper()
	var err error
	other := f.session
	other.User.ID = "other-merchant"
	if _, err = f.db.OrderLabelArtifact(identity.WithSession(f.ctx, other), workID); !errors.Is(err, orders.ErrNotFound) {
		t.Fatalf("cross-user label read: %v", err)
	}
	if _, err = f.db.Pool.Exec(f.ctx, `UPDATE saas_sessions SET revoked_at=clock_timestamp() WHERE audit_id=$1`, f.session.AuditID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.OrderLabelArtifact(f.ctx, workID); !errors.Is(err, orders.ErrNotFound) {
		t.Fatalf("revoked session read label: %v", err)
	}
}

func assertStoredLabel(t *testing.T, f ordersStoreFixture, workID string, protector orderprivacy.LabelProtector, pdf []byte) {
	t.Helper()
	stored, err := f.db.OrderLabelArtifact(f.ctx, workID)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := protector.OpenOrderLabel(stored)
	if err != nil || !bytes.Equal(actual, pdf) {
		t.Fatal("stored PDF did not decrypt")
	}
	if bytes.Contains(stored.Ciphertext, []byte("SYNTHETIC_RECEIVER")) {
		t.Fatal("plaintext label persisted")
	}
}

func assertLabelRollbackRejected(t *testing.T, f ordersStoreFixture) {
	t.Helper()
	dsn := f.db.Pool.Config().ConnString()
	catalogSyncMigrate(t, dsn, "down-to", "52")
	ctx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", dsn, "down")
	output, err := cmd.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("encrypted label artifacts prevent destructive rollback")) {
		t.Fatal("label migration did not reject destructive rollback for the expected reason")
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_label_artifacts`, 1)
	catalogSyncMigrate(t, dsn, "up")
	if err := f.db.RequireCurrentSchema(f.ctx); err != nil {
		t.Fatal("rejected rollback changed schema")
	}
}

func TestOrdersLabelRetentionDeletesExpiredCiphertext(t *testing.T) {
	f := newOrdersStoreFixture(t)
	w, _ := labelStoreWork(t, f)
	// Seed an already aged ciphertext only in this disposable test schema.
	_, err := f.db.Pool.Exec(f.ctx, `INSERT INTO orders_label_artifacts(work_id,tenant_id,moysklad_account,ozon_account,task_id,label_type,digest,plain_size,ciphertext,created_at,expires_at) VALUES($1,$2,$3,$4,'99','big_label',$5,32,$6,clock_timestamp()-interval '2 hours',clock_timestamp()-interval '1 hour')`, w.ID, w.Scope.Tenant, w.Scope.MoyskladAccount, w.Scope.OzonAccount, string(bytes.Repeat([]byte{'a'}, 64)), bytes.Repeat([]byte{7}, 29))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Pool.Exec(f.ctx, `UPDATE orders_label_artifacts SET task_id='100' WHERE work_id=$1`, w.ID); err == nil {
		t.Fatal("immutable label changed")
	}
	if _, err = f.db.OrderLabelArtifact(f.ctx, w.ID); !errors.Is(err, orders.ErrNotFound) {
		t.Fatal("expired label exposed")
	}
	count, err := f.db.DeleteExpiredOrderSecrets(f.ctx, time.Now(), 10)
	if err != nil || count != 1 {
		t.Fatalf("label retention: deleted=%d error=%v", count, err)
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_label_artifacts`, 0)
}

func assertLabelHTTPIsolation(t *testing.T, f ordersStoreFixture, workID string, pdf []byte) {
	t.Helper()
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{31}, 32)))
	mailbox := &testMailbox{tokens: map[string]string{}}
	handler, err := console.MerchantHandler(f.db, "http://127.0.0.1:5173", mailbox)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	owner := ownerLabelBrowser(t, f, server.URL)
	path := orders.LabelArtifactURL(workID)
	if status, raw := owner.request(t, http.MethodGet, path, "", false); status != http.StatusOK || !bytes.Equal(raw, pdf) {
		t.Fatalf("owner download: HTTP %d", status)
	}
	other := newMerchantBrowser(t, server.URL)
	other.registerAndLogin(t, mailbox, "label-other@example.test")
	if status, raw := other.request(t, http.MethodGet, path, "", false); status != http.StatusNotFound || bytes.Contains(raw, pdf) {
		t.Fatalf("foreign PDF exposed: HTTP %d", status)
	}
	if status, _ := owner.request(t, http.MethodDelete, "/api/session", "", true); status != http.StatusNoContent {
		t.Fatalf("logout: %d", status)
	}
	if status, _ := owner.request(t, http.MethodGet, path, "", false); status != http.StatusUnauthorized {
		t.Fatalf("revoked browser downloaded PDF: %d", status)
	}
}

func ownerLabelBrowser(t *testing.T, f ordersStoreFixture, serverURL string) *merchantBrowser {
	t.Helper()
	identityStore, err := identity.New(f.db.Pool, f.db.Timeout)
	if err != nil {
		t.Fatal(err)
	}
	session, token, err := identityStore.CreateSession(f.ctx, f.session.User)
	if err != nil {
		t.Fatal(err)
	}
	owner := newMerchantBrowser(t, serverURL)
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	owner.client.Jar.SetCookies(u, []*http.Cookie{{Name: "styk_session", Value: token, Path: "/", HttpOnly: true}})
	owner.csrf = session.CSRF
	return owner
}

func labelStoreWork(t *testing.T, f ordersStoreFixture) (orders.Work, orders.Receipt) {
	t.Helper()
	before := ordersStorePosting(f.config.Scope, "label-posting", "source", time.Now().UTC())
	ordersStoreCommit(t, f, before, "label-source")
	p := splitStorePreview(t, f, before)
	p.ID += "-label"
	c := &p.Decisions[0].Commands[0]
	c.ID += "-label"
	c.IdempotencyKey = c.ID
	c.Action = orders.ActionPostingLabelCreate
	c.PostingAction.Parameters = orders.ActionParameters{LabelType: "big_label"}
	p, err := f.db.SaveOrderPreview(f.ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	ordersStoreApprove(t, f, p)
	w := ordersStoreClaim(t, f, time.Minute)
	allowed, err := f.db.GuardOrderDispatch(f.ctx, orders.DispatchGuard{Work: w, Begin: true, Now: time.Now()})
	if err != nil || !allowed {
		t.Fatalf("guard: %v", err)
	}
	r := orders.Receipt{CommandID: w.Command.ID, State: orders.StatePolling, TaskID: "99", TaskIDs: []orders.ActionTask{{ID: "99", Type: "big_label"}}, LabelTasksVerified: true, HTTPStatus: 200, ObservedAt: time.Now().UTC()}
	ack := orders.Completion{WorkID: w.ID, Scope: w.Scope, ClaimToken: w.ClaimToken, ExpectedState: orders.StateDispatching, State: orders.StatePolling, Receipt: r}
	if err = f.db.CompleteOrderWork(f.ctx, ack); err != nil {
		t.Fatal(err)
	}
	return ordersStoreClaim(t, f, time.Minute), r
}
