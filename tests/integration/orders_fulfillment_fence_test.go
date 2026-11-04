//go:build integration

package integration

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/orders"
)

// Exercise the database fence independently of the current mapping UI, which
// also rejects duplicate enrolled targets. A later mapping transfer must not
// release an unresolved Ozon action belonging to a different source account.
func TestOrdersFulfillmentFenceSpansMoyskladAccounts(t *testing.T) {
	f := newOrdersStoreFixture(t)
	posting := ordersStorePosting(f.config.Scope, "shared-posting", "source", time.Now().UTC())
	posting.Requirements = &orders.PackingRequirements{Presence: orders.PresenceValue, Products: make(map[orders.PackingRequirementKind]orders.Field[[]string])}
	for _, kind := range orders.PackingRequirementKinds() {
		posting.Requirements.Products[kind] = orders.Field[[]string]{Presence: orders.PresenceEmpty}
	}
	ordersStoreCommit(t, f, posting, "fence-source")
	op := ordersStoreApprove(t, f, splitStorePreview(t, f, posting))
	second := secondMoyOrderFixture(t, f)
	seedSecondOrderParents(t, f, second, op.ID)
	_, err := f.db.Pool.Exec(f.ctx, `INSERT INTO orders_work(id,operation_id,tenant_id,moysklad_account,ozon_account,config_revision,posting_number,command_id,command_fingerprint,action,state,work,created_at,updated_at) SELECT 'second-work','second-operation',tenant_id,$2,ozon_account,1,posting_number,'second-command',command_fingerprint,action,state,jsonb_set(jsonb_set(jsonb_set(jsonb_set(replace(work::text,$3::text,$2::text)::jsonb,'{id}','"second-work"'),'{operation_id}','"second-operation"'),'{command,id}','"second-command"'),'{scope,moysklad_account}',to_jsonb($2::text)),created_at,updated_at FROM orders_work WHERE operation_id=$1`, op.ID, second.config.Scope.MoyskladAccount, f.config.Scope.MoyskladAccount)
	var constraint *pgconn.PgError
	if !errors.As(err, &constraint) || constraint.Code != "23505" || constraint.ConstraintName != "orders_work_active_fulfillment_target" {
		t.Fatalf("Ozon target fence missing across source accounts: %v", err)
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_work WHERE action='posting_split'`, 1)
}

func secondMoyOrderFixture(t *testing.T, f ordersStoreFixture) ordersStoreFixture {
	t.Helper()
	second := f
	second.config.Scope.MoyskladAccount = "orders-moy-second"
	second.config.Revision = 0
	scope := second.config.Scope
	if err := f.db.RegisterStockConnection(f.ctx, scope.Tenant, "moysklad", scope.MoyskladAccount, "second synthetic source", true); err != nil {
		t.Fatal(err)
	}
	_, err := f.db.Pool.Exec(f.ctx, `INSERT INTO connection_credentials(tenant_id,provider,account_id,name,revision,ciphertext,checked_at,last_request_id) VALUES($1,'moysklad',$2,'Synthetic',1,$3,clock_timestamp(),'second-source')`, scope.Tenant, scope.MoyskladAccount, make([]byte, 29))
	if err != nil {
		t.Fatal(err)
	}
	second.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: second.config, Actor: f.session.AuditID, IdempotencyKey: "second-order-source"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return second
}

func seedSecondOrderParents(t *testing.T, f, second ordersStoreFixture, operation string) {
	t.Helper()
	account := second.config.Scope.MoyskladAccount
	queries := []string{
		`INSERT INTO orders_projection_bindings(tenant_id,moysklad_account,ozon_account,posting_number,sync_id,marker,created_at) SELECT tenant_id,$2,ozon_account,posting_number,sync_id,marker,created_at FROM orders_projection_bindings WHERE moysklad_account=$3`,
		`INSERT INTO orders_previews(id,tenant_id,moysklad_account,ozon_account,config_revision,preview,fingerprint,actor,automatic,owner_user_id,created_at,expires_at) SELECT 'second-preview',p.tenant_id,$2,p.ozon_account,1,jsonb_set(jsonb_set(p.preview,'{id}','"second-preview"'),'{scope,moysklad_account}',to_jsonb($2::text)),p.fingerprint,p.actor,p.automatic,p.owner_user_id,p.created_at,p.expires_at FROM orders_previews p JOIN orders_operations o ON o.preview_id=p.id WHERE o.id=$1`,
		`INSERT INTO orders_operations(id,tenant_id,moysklad_account,ozon_account,config_revision,preview_id,actor,owner_user_id,automatic,request_key,request_hash,created_at) SELECT 'second-operation',tenant_id,$2,ozon_account,1,'second-preview',actor,owner_user_id,automatic,'second-request',request_hash,created_at FROM orders_operations WHERE id=$1`,
	}
	for i, q := range queries {
		args := []any{operation, account}
		if i == 0 {
			args = append(args, f.config.Scope.MoyskladAccount)
			q = "WITH input AS (SELECT $1::text) " + q
		}
		if _, err := f.db.Pool.Exec(f.ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
}
