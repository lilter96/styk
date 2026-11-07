//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/identity"
)

func TestIdentityCachedDummyHashNeverAuthenticatesUnknownUser(t *testing.T) {
	db := catalogSyncDB(t)
	for range 2 {
		store, err := identity.New(db.Pool, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		user, err := store.Authenticate(context.Background(), "unknown@example.test", "styk-dummy-password-never-used-for-login")
		if !errors.Is(err, identity.ErrAuth) || user.ID != "" {
			t.Fatal("dummy comparison match authenticated a missing identity", user.ID, err)
		}
	}
}
