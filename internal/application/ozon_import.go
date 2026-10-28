package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"example.com/styk/internal/domain"
	"fmt"
	"sort"
)

type OzonReader interface {
	Read(context.Context) (domain.OzonImport, error)
}
type OzonStore interface {
	SaveOzonImport(context.Context, domain.OzonImport, string) (bool, error)
}
type OzonImporter struct {
	Reader OzonReader
	Store  OzonStore
}

func (i OzonImporter) Run(ctx context.Context) (bool, error) {
	snap, err := i.Reader.Read(ctx)
	if err != nil {
		return false, fmt.Errorf("collect Ozon data: %w", err)
	}
	sort.Slice(snap.Products, func(a, b int) bool { return snap.Products[a].ProductID < snap.Products[b].ProductID })
	sort.Slice(snap.Warehouses, func(a, b int) bool { return snap.Warehouses[a].ID < snap.Warehouses[b].ID })
	sort.Slice(snap.Stocks, func(a, b int) bool {
		x, y := snap.Stocks[a], snap.Stocks[b]
		if x.ProductID != y.ProductID {
			return x.ProductID < y.ProductID
		}
		return x.WarehouseID < y.WarehouseID
	})
	b, err := json.Marshal(struct {
		Products   []domain.OzonProduct
		Warehouses []domain.OzonWarehouse
		Stocks     []domain.OzonStock
	}{snap.Products, snap.Warehouses, snap.Stocks})
	if err != nil {
		return false, fmt.Errorf("fingerprint Ozon import: %w", err)
	}
	hash := sha256.Sum256(b)
	created, err := i.Store.SaveOzonImport(ctx, snap, hex.EncodeToString(hash[:]))
	if err != nil {
		return false, fmt.Errorf("persist Ozon import: %w", err)
	}
	return created, nil
}
