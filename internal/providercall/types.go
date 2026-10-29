// Package providercall coordinates provider attempts across processes. Admission
// and completion use short database transactions; timers and HTTP never do.
package providercall

import (
	"context"
	"errors"
	"net/http"
	"time"
)

var (
	ErrInvalid             = errors.New("invalid provider call scope")
	ErrCredentialsRejected = errors.New("provider credential revision rejected")
	ErrLeaseLost           = errors.New("provider call permit expired")
	ErrAlreadyStarted      = errors.New("provider call permit already started")
	ErrCooldownUnbounded   = errors.New("provider retry delay requires operator review")
	ErrMethodForbidden     = errors.New("provider operation permission is temporarily denied")
)

// Scope is bound by the credential factory after tenant authorization. PendingID
// is only for connection probes whose external account has not been discovered.
type Scope struct {
	Tenant             string
	Provider           string
	Account            string
	CredentialRevision int64
	PendingID          string
}

type Request struct {
	Scope     Scope
	Method    string
	Operation string
	Timeout   time.Duration
	IsWrite   bool
	TargetKey string
	// Endpoint is inspected only to allow the synthetic timing profile on loopback.
	// It is never persisted.
	Endpoint string
}

type Options struct {
	// SyntheticInterval can accelerate loopback fixtures only. Real hosts always
	// retain the one-second account interval.
	SyntheticInterval       time.Duration
	MaxInflight             int
	MethodForbiddenCooldown time.Duration
}

// Attempt contains only bounded metadata, never a URL, query, body or credential.
type Attempt struct {
	ID             string
	Scope          Scope
	Method         string
	Operation      string
	IsWrite        bool
	TargetKey      string
	Interval       time.Duration
	TargetInterval time.Duration
	MaxInflight    int
	LeaseDuration  time.Duration
}

type Lease struct{ Attempt Attempt }
type Admission struct {
	Lease Lease
	Delay time.Duration
}

// Result encodes delays relative to the database clock. A future HTTP date is
// carried separately so application clock skew cannot admit an early retry.
type Result struct {
	Status             int
	Cooldown           time.Duration
	CooldownUntil      time.Time
	MethodCooldown     time.Duration
	IndefiniteCooldown bool
}

type Store interface {
	TryAcquireProviderCall(context.Context, Attempt) (Admission, error)
	StartProviderCall(context.Context, Lease) (time.Duration, error)
	FinishProviderCall(context.Context, Lease, Result) error
}

// Gate is the consumer port used immediately before an actual HTTP attempt.
type Gate interface {
	Begin(context.Context, Request) (*Permit, error)
}

// Finish reports coordination failures independently from provider acceptance.
// Callers must preserve an acknowledged provider result if this returns an error.
func (p *Permit) Finish(ctx context.Context, status int, headers http.Header) error {
	return p.finish(ctx, responseResult(status, headers, p.forbiddenCooldown))
}

func (p *Permit) Abort(ctx context.Context) error { return p.finish(ctx, Result{}) }
