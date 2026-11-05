//go:build integration

package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
)

func settingsFixture(t *testing.T) (*credentialFixture, credentials.Scope) {
	t.Helper()
	f := newCredentialFixture(t)
	in := map[string]any{"tenant": f.tenant, "account": "", "name": "Original", "secret": "synthetic-moy-secret", "request_id": "settings-create", "expected_revision": 0, "acknowledge": true}
	f.request(t, "POST", "/api/integrations/moysklad/credentials", in, http.StatusOK)
	return f, credentials.Scope{Tenant: f.tenant, Provider: "moysklad", Account: f.moy}
}

func TestConnectionSettingsPersistenceIsolationAndCredentialIndependence(t *testing.T) {
	f, scope := settingsFixture(t)
	ctx := context.Background()
	before, err := f.db.LoadCredential(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	calls := f.calls.Load()
	path := "/api/integrations/moysklad/settings?tenant=" + f.tenant + "&account=" + f.moy
	f.request(t, "GET", path, nil, 200)
	in := map[string]any{"tenant": f.tenant, "account": f.moy, "name": "Основной аккаунт", "description": "Склад для розницы", "revision": 0}
	var saved domain.ConnectionSettings
	if err = json.Unmarshal(f.request(t, "POST", "/api/integrations/moysklad/settings", in, 200), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Name != in["name"] || saved.Description != in["description"] || saved.Revision != 1 {
		t.Fatal("settings not saved")
	}
	f.request(t, "POST", "/api/integrations/moysklad/settings", in, 409)
	in["tenant"] = "foreign-" + f.tenant
	f.request(t, "POST", "/api/integrations/moysklad/settings", in, 404)
	f.request(t, "GET", "/api/integrations/moysklad/settings?tenant=foreign-"+f.tenant+"&account="+f.moy, nil, 404)
	in["tenant"], in["revision"], in["name"] = f.tenant, 1, strings.Repeat("я", 121)
	f.request(t, "POST", "/api/integrations/moysklad/settings", in, 400)
	actual, err := f.db.ConnectionSettings(ctx, scope)
	if err != nil || actual != saved {
		t.Fatal("failed mutation changed settings")
	}
	after, err := f.db.LoadCredential(ctx, scope)
	if err != nil || after.Revision != before.Revision || !bytes.Equal(after.Ciphertext, before.Ciphertext) || f.calls.Load() != calls {
		t.Fatal("metadata edit altered secret or contacted provider")
	}
	if key, err := f.vault.Decrypt(after); err != nil || key != "synthetic-moy-secret" {
		t.Fatal("credential no longer decrypts")
	}
}

func TestConnectionSettingsConcurrentEditsAndRollback(t *testing.T) {
	f, scope := settingsFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"First", "Second"} {
		wg.Go(func() {
			_, err := f.db.SaveConnectionSettings(ctx, scope, domain.ConnectionSettings{Name: name}, "local test operator")
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
			continue
		}
		if errors.Is(err, credentials.ErrVersion) {
			conflicts++
			continue
		}
		t.Fatal(err)
	}
	if success != 1 || conflicts != 1 {
		t.Fatal("concurrent edits were not fenced")
	}
	assertSettingsRollback(t, f, scope)
}

func assertSettingsRollback(t *testing.T, f *credentialFixture, scope credentials.Scope) {
	t.Helper()
	ctx := context.Background()
	before, err := f.db.ConnectionSettings(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE connection_settings SET revision=-1 WHERE tenant_id=$1 AND account_id=$2`, scope.Tenant, scope.Account); err == nil {
		t.Fatal("database accepted invalid revision")
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := f.db.ConnectionSettings(ctx, scope)
	if err != nil || after != before {
		t.Fatal("failed transaction leaked changes")
	}
}

func TestConnectionSettingsArchivePreservesEvidence(t *testing.T) {
	f, scope := settingsFixture(t)
	ctx := context.Background()
	saved, err := f.db.SaveConnectionSettings(ctx, scope, domain.ConnectionSettings{Name: "Retired"}, "local test operator")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Pool.Exec(ctx, `UPDATE connection_settings SET archived=true WHERE tenant_id=$1 AND source=$2 AND account_id=$3`, scope.Tenant, scope.Provider, scope.Account); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.ConnectionSettings(ctx, scope); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("archived profile remains active")
	}
	if _, err = f.db.SaveConnectionSettings(ctx, scope, saved, "local test operator"); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("ordinary metadata edit reactivated archive")
	}
	if _, err = f.db.LoadCredential(ctx, scope); err != nil {
		t.Fatal("archive destroyed credential history")
	}
}
