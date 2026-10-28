package application

import (
	"context"
	"errors"
	"example.com/styk/internal/domain"
	"testing"
)

type readerStub struct {
	snap domain.InventoryImport
	err  error
}

func (r readerStub) Read(context.Context, string) (domain.InventoryImport, error) {
	return r.snap, r.err
}

type storeStub struct {
	called bool
	err    error
}

func (s *storeStub) SaveInventoryImport(context.Context, domain.InventoryImport, string) (bool, error) {
	s.called = true
	return false, s.err
}
func TestImportFailures(t *testing.T) {
	s := &storeStub{}
	_, err := (Importer{Reader: readerStub{err: errors.New("incomplete")}, Store: s}).Run(context.Background(), "x")
	if err == nil || s.called {
		t.Fatal("incomplete collection was persisted")
	}
	s.err = errors.New("database down")
	_, err = (Importer{Reader: readerStub{snap: domain.InventoryImport{Account: "x"}}, Store: s}).Run(context.Background(), "x")
	if err == nil || !s.called {
		t.Fatal("database failure not returned")
	}
}
