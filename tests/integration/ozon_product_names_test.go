//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/testpublication"
)

func TestOzonProductNamesPreserveImmutableSnapshotHistory(t *testing.T) {
	db, _ := controlledFixtureWithMapping(t, false)
	ctx := context.Background()
	snap := domain.OzonImport{Account: testpublication.OzonAccount, StartedAt: time.Now(), FinishedAt: time.Now(), Products: []domain.OzonProduct{{ProductID: "501", OfferID: "named-offer", Name: "Этикетка для склада"}}}
	save := func(wantCreated bool) string {
		t.Helper()
		fingerprint, err := application.OzonFingerprint(&snap)
		if err != nil {
			t.Fatal(err)
		}
		if created, err := db.SaveOzonImport(ctx, snap, fingerprint); err != nil || created != wantCreated {
			t.Fatalf("created=%v want=%v error=%v", created, wantCreated, err)
		}
		return fingerprint
	}
	first := save(true)
	snap.StartedAt = snap.StartedAt.Add(time.Second)
	snap.FinishedAt = snap.FinishedAt.Add(time.Second)
	if repeated := save(false); repeated != first {
		t.Fatal("unchanged product metadata invalidated content identity")
	}
	snap.Products[0].Name = "Этикетка новая"
	if changed := save(true); changed == first {
		t.Fatal("changed observed name reused historical content identity")
	}
	var names []string
	rows, err := db.Pool.Query(ctx, `SELECT name FROM ozon_product_observations p JOIN source_imports i ON i.id=p.import_id WHERE i.account_id=$1 AND p.product_id='501' ORDER BY import_id`, snap.Account)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if rows.Err() != nil || len(names) != 2 || names[0] != "Этикетка для склада" || names[1] != snap.Products[0].Name {
		t.Fatalf("historical names not preserved: %v error=%v", names, rows.Err())
	}
}

func TestOzonProductNameLimitRollsBackEntireImport(t *testing.T) {
	db, _ := controlledFixtureWithMapping(t, false)
	ctx := context.Background()
	snap := domain.OzonImport{Account: testpublication.OzonAccount, StartedAt: time.Now(), FinishedAt: time.Now(), Products: []domain.OzonProduct{{ProductID: "501", OfferID: "valid", Name: "Valid"}, {ProductID: "502", OfferID: "invalid", Name: strings.Repeat("я", 2049)}}}
	if _, err := db.SaveOzonImport(ctx, snap, "invalid-name-size"); err == nil {
		t.Fatal("oversized product metadata persisted")
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM source_imports WHERE fingerprint='invalid-name-size'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial batch persisted: count=%d error=%v", count, err)
	}
}
