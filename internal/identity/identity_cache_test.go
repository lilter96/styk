package identity

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func TestIdentityStoreConstructorPreservesCostAndMutationIsolation(t *testing.T) {
	t.Parallel()
	pool := &pgxpool.Pool{}
	first, err := New(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	wanted := bytes.Clone(second.dummyHash)
	first.dummyHash[0] ^= 1
	third, err := New(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(second.dummyHash, wanted) || !bytes.Equal(third.dummyHash, wanted) {
		t.Fatal("one store mutated another store or the cached comparison hash")
	}
	for _, store := range []*Store{second, third} {
		cost, err := bcrypt.Cost(store.dummyHash)
		if err != nil || cost != 12 {
			t.Fatal("dummy comparison cost changed", cost, err)
		}
		if store.pool != pool || store.timeout != time.Second || cap(store.hashSlots) != 4 {
			t.Fatal("constructor changed store-local settings")
		}
	}
	if first.hashSlots == second.hashSlots || second.hashSlots == third.hashSlots {
		t.Fatal("password concurrency slots leaked between stores")
	}
}

func TestIdentityStoreConstructorConcurrentCopies(t *testing.T) {
	t.Parallel()
	var wait sync.WaitGroup
	errorsOut := make(chan error, 16)
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			store, err := New(&pgxpool.Pool{}, time.Second)
			if err == nil {
				store.dummyHash[0] ^= 1
			}
			errorsOut <- err
		}()
	}
	wait.Wait()
	for range 16 {
		if err := <-errorsOut; err != nil {
			t.Fatal(err)
		}
	}
	store, err := New(&pgxpool.Pool{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if cost, err := bcrypt.Cost(store.dummyHash); err != nil || cost != 12 {
		t.Fatal("concurrent store mutation corrupted cached hash", cost, err)
	}
}

var identityConstructorBenchmark *Store

func BenchmarkIdentityStoreConstructor(b *testing.B) {
	pool := &pgxpool.Pool{}
	if _, err := New(pool, time.Second); err != nil {
		b.Fatal(err)
	}
	b.Run("before_uncached_dummy_hash", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			hash, err := bcrypt.GenerateFromPassword([]byte("styk-dummy-password-never-used-for-login"), 12)
			if err != nil {
				b.Fatal(err)
			}
			identityConstructorBenchmark = &Store{pool: pool, timeout: time.Second, dummyHash: hash, hashSlots: make(chan struct{}, 4)}
		}
	})
	b.Run("after_cached_dummy_hash", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			store, err := New(pool, time.Second)
			if err != nil {
				b.Fatal(err)
			}
			identityConstructorBenchmark = store
		}
	})
}
