package console

import (
	"encoding/hex"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/providercall"
)

type coordinatedProvider interface {
	Coordinate(providercall.Gate, providercall.Scope) error
}

func (c *connections) coordinate(client coordinatedProvider, scope credentials.Scope, revision int64, pendingSecret string) error {
	gate, err := providercall.New(c.db, providercall.Options{SyntheticInterval: 20 * time.Millisecond})
	if err != nil {
		return err
	}
	call := providercall.Scope{Tenant: scope.Tenant, Provider: scope.Provider, Account: scope.Account, CredentialRevision: revision}
	if scope.Account == "" {
		call.PendingID = hex.EncodeToString(c.vault.Digest([]byte(pendingSecret)))
	}
	return client.Coordinate(gate, call)
}
func (c *connections) scopedOzon(scope credentials.Scope, key string, revision int64) (*ozon.Client, error) {
	client, err := c.newOzon(scope.Account, key)
	if err == nil {
		err = c.coordinate(client, scope, revision, "")
	}
	return client, err
}
func (c *connections) scopedMoy(scope credentials.Scope, key string, revision int64) (*moysklad.Client, error) {
	client, err := c.newMoy(key)
	if err == nil {
		err = c.coordinate(client, scope, revision, "")
	}
	return client, err
}
