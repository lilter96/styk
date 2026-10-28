package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"example.com/styk/internal/domain"
)

type InventoryReader interface {
	Read(context.Context, string) (domain.InventoryImport, error)
}
type InventoryStore interface {
	SaveInventoryImport(context.Context, domain.InventoryImport, string) (bool, error)
}
type Importer struct {
	Reader InventoryReader
	Store  InventoryStore
}

func (i Importer) Run(ctx context.Context, account string) (bool, error) {
	snap, err := i.Reader.Read(ctx, account)
	if err != nil {
		return false, fmt.Errorf("collect MoySklad data: %w", err)
	}
	sort.Slice(snap.Entities, func(a, b int) bool {
		x, y := snap.Entities[a], snap.Entities[b]
		if x.Kind != y.Kind {
			return x.Kind < y.Kind
		}
		return x.ID < y.ID
	})
	sort.Slice(snap.Stock, func(a, b int) bool {
		x, y := snap.Stock[a], snap.Stock[b]
		if x.Kind != y.Kind {
			return x.Kind < y.Kind
		}
		return x.ExternalID < y.ExternalID
	})
	canonical, err := json.Marshal(struct {
		Entities []domain.ExternalEntity
		Stock    []domain.StockObservation
	}{snap.Entities, snap.Stock})
	if err != nil {
		return false, fmt.Errorf("encode import fingerprint: %w", err)
	}
	sum := sha256.Sum256(canonical)
	created, err := i.Store.SaveInventoryImport(ctx, snap, hex.EncodeToString(sum[:]))
	if err != nil {
		return false, fmt.Errorf("persist import: %w", err)
	}
	return created, nil
}
