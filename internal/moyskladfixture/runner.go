package moyskladfixture

import (
	"context"
	"errors"
	"fmt"
)

// Remote is implemented only by a fixture-specific adapter. Search must return
// every exact match or an error if coverage is uncertain; Verify independently
// reads and validates the expected fields. No errors may contain API payloads.
type Remote interface {
	preflight(context.Context, string) (map[string]int, error)
	checkNamespace(context.Context, Manifest) error
	prepare(context.Context, Operation, Manifest) error
	search(context.Context, Operation) ([]string, error)
	create(context.Context, Operation, Manifest) (string, error)
	verify(context.Context, Operation, string, Manifest) error
}

var ErrUnknownOutcome = errors.New("fixture creation outcome is unknown; reconcile before proceeding")
var ErrDuplicate = errors.New("fixture identity is already present or ambiguous")
var ErrOperationLimit = errors.New("fixture POST limit reached; remaining operations are planned")
var ErrUnsupported = errors.New("fixture prerequisite unavailable")
var ErrCoverageUnknown = errors.New("characteristic collection coverage is unknown")

// EnableVariantContinuation only extends this existing, fully verified
// product-only fixture. It cannot reset a sent or uncertain operation.
func EnableVariantContinuation(l *Lock, m *Manifest, account string) error {
	if err := validateManifest(*m, l.namespace); err != nil {
		return err
	}
	hash, err := AccountHash(account)
	if err != nil || hash != m.AccountHash || !m.NamespaceChecked {
		return errors.New("fixture account or namespace does not match")
	}
	base, _ := Plan(m.Namespace)
	if len(m.Operations) != len(base) {
		return errors.New("fixture continuation already enabled")
	}
	for _, o := range m.Operations {
		switch o.Key {
		case "variant-characteristic", "variant-one", "variant-two":
			if o.State != Skipped || o.RemoteID != "" || o.Note != "fixture prerequisite unavailable" {
				return errors.New("variant operation was not safely skipped")
			}
		default:
			if o.State != Created || o.RemoteID == "" {
				return errors.New("existing fixture is not fully verified")
			}
		}
	}
	for i := range m.Operations {
		switch m.Operations[i].Key {
		case "variant-characteristic", "variant-one", "variant-two":
			m.Operations[i].State = Planned
			m.Operations[i].Note = "explicitly enabled from never-sent skipped operation"
		}
	}
	m.MultipleOrderProductOnly = true
	extension, err := VariantExtension(m.Namespace)
	if err != nil {
		return err
	}
	m.Operations = append(m.Operations, extension...)
	m.StockMayBeStale = true
	return l.Save(*m)
}

// EnableSkippedShipment can replan only a never-sent shipment after the
// implementation is ready. UNKNOWN_OUTCOME is never reset by this operation.
func EnableSkippedShipment(l *Lock, m *Manifest, account string) error {
	if err := validateManifest(*m, l.namespace); err != nil {
		return err
	}
	hash, err := AccountHash(account)
	if err != nil || hash != m.AccountHash {
		return errors.New("fixture account does not match its manifest")
	}
	for _, key := range []string{"warehouse-a", "product-b", "counterparty", "stock-a", "order-partial"} {
		if _, ok := findID(*m, key); !ok {
			return errors.New("shipment prerequisite not verified")
		}
	}
	for i := range m.Operations {
		o := &m.Operations[i]
		if o.Key != "shipment-partial" {
			continue
		}
		if o.State != Skipped || o.RemoteID != "" {
			return errors.New("only a never-sent skipped shipment can be enabled")
		}
		o.State = Planned
		o.Note = "explicitly enabled after prerequisite verification"
		if len(m.StockChecks) > 0 {
			m.StockMayBeStale = true
		}
		return l.Save(*m)
	}
	return errors.New("shipment operation missing")
}

// EnableSkippedMultipleOrder explicitly permits a product-only fallback after
// the variant prerequisite was safely skipped, never after an unknown outcome.
func EnableSkippedMultipleOrder(l *Lock, m *Manifest, account string) error {
	if err := validateManifest(*m, l.namespace); err != nil {
		return err
	}
	hash, err := AccountHash(account)
	if err != nil || hash != m.AccountHash {
		return errors.New("fixture account does not match its manifest")
	}
	for _, key := range []string{"warehouse-a", "product-a", "product-c", "counterparty"} {
		if _, ok := findID(*m, key); !ok {
			return errors.New("multi-position order prerequisite not verified")
		}
	}
	variantSkipped := false
	for _, o := range m.Operations {
		if o.Key == "variant-one" && o.State == Skipped {
			variantSkipped = true
		}
	}
	if !variantSkipped {
		return errors.New("variant prerequisite is not safely skipped")
	}
	for i := range m.Operations {
		o := &m.Operations[i]
		if o.Key != "order-multiple" {
			continue
		}
		if o.State != Skipped || o.RemoteID != "" {
			return errors.New("only a never-sent skipped order can be enabled")
		}
		o.State = Planned
		o.Note = "explicitly enabled product-only fallback"
		if len(m.StockChecks) > 0 {
			m.StockMayBeStale = true
		}
		return l.Save(*m)
	}
	return errors.New("multi-position order missing")
}

// Run never retries a POST with an unknown outcome, even when a search returns
// no rows: absence may reflect delayed visibility, filtering or permissions.
func Run(ctx context.Context, l *Lock, m *Manifest, account string, remote Remote, maxPosts int) error {
	if remote == nil || maxPosts < 1 || maxPosts > len(m.Operations) {
		return errors.New("valid remote and bounded operation limit required")
	}
	if err := validateManifest(*m, l.namespace); err != nil {
		return err
	}
	hash, err := AccountHash(account)
	if err != nil || hash != m.AccountHash {
		return errors.New("fixture account does not match its manifest")
	}
	counts, err := remote.preflight(ctx, account)
	if err != nil {
		return errors.New("test-account preflight failed; no creation request was sent")
	}
	if m.InitialCounts == nil {
		m.InitialCounts = counts
		if err = l.Save(*m); err != nil {
			return err
		}
	}
	if !m.NamespaceChecked {
		if err = remote.checkNamespace(ctx, *m); err != nil {
			if errors.Is(err, ErrDuplicate) {
				return fmt.Errorf("fixture namespace preflight: %w", ErrDuplicate)
			}
			return errors.New("fixture namespace preflight incomplete; no creation request was sent")
		}
		m.NamespaceChecked = true
		if err = l.Save(*m); err != nil {
			return err
		}
	}
	posts := 0
	for i := range m.Operations {
		o := &m.Operations[i]
		if o.State == Skipped {
			continue
		}
		if err := ctx.Err(); err != nil {
			if o.State == Planned {
				o.State = FailedBeforeSend
				o.Note = "cancelled before sending"
				if saveErr := l.Save(*m); saveErr != nil {
					return saveErr
				}
			}
			return err
		}
		if o.State == Unknown || o.State == Created {
			// Characteristics have no reliable complete search contract. A saved
			// returned ID can be verified directly; without one never retry.
			if o.Kind == "characteristic" {
				if o.RemoteID == "" || remote.verify(ctx, *o, o.RemoteID, *m) != nil {
					o.State = Unknown
					o.Note = "characteristic identity cannot be verified"
					if saveErr := l.Save(*m); saveErr != nil {
						return saveErr
					}
					return fmt.Errorf("reconcile %s: %w", o.Key, ErrUnknownOutcome)
				}
				o.State = Created
				o.Note = ""
				if err = l.Save(*m); err != nil {
					return err
				}
				continue
			}
			matches, err := remote.search(ctx, *o)
			if err != nil {
				o.State = Unknown
				o.Note = "identity reconciliation incomplete"
				if saveErr := l.Save(*m); saveErr != nil {
					return saveErr
				}
				return fmt.Errorf("reconcile %s: %w", o.Key, ErrUnknownOutcome)
			}
			if len(matches) != 1 || !uuidPattern.MatchString(matches[0]) || (o.RemoteID != "" && o.RemoteID != matches[0]) {
				o.State = Unknown
				o.Note = "identity not uniquely confirmed"
				if saveErr := l.Save(*m); saveErr != nil {
					return saveErr
				}
				return fmt.Errorf("reconcile %s: %w", o.Key, ErrUnknownOutcome)
			}
			o.RemoteID = matches[0]
			if err = l.Save(*m); err != nil {
				return err
			}
			if err = remote.verify(ctx, *o, o.RemoteID, *m); err != nil {
				o.State = Unknown
				o.Note = "independent verification failed"
				if saveErr := l.Save(*m); saveErr != nil {
					return saveErr
				}
				return fmt.Errorf("verify %s: %w", o.Key, ErrUnknownOutcome)
			}
			o.State = Created
			o.Note = ""
			if err = l.Save(*m); err != nil {
				return err
			}
			continue
		}
		if posts >= maxPosts {
			return ErrOperationLimit
		}
		if err := remote.prepare(ctx, *o, *m); err != nil {
			if errors.Is(err, ErrUnsupported) {
				o.State = Skipped
				o.Note = "fixture prerequisite unavailable"
				if saveErr := l.Save(*m); saveErr != nil {
					return saveErr
				}
				continue
			}
			o.State = FailedBeforeSend
			o.Note = "preparation failed before sending"
			if saveErr := l.Save(*m); saveErr != nil {
				return saveErr
			}
			return fmt.Errorf("prepare %s failed before sending", o.Key)
		}
		matches, err := remote.search(ctx, *o)
		if err != nil && !(o.Kind == "characteristic" && m.MultipleOrderProductOnly && errors.Is(err, ErrCoverageUnknown)) {
			o.State = FailedBeforeSend
			o.Note = "identity preflight incomplete"
			if saveErr := l.Save(*m); saveErr != nil {
				return saveErr
			}
			return fmt.Errorf("preflight %s failed before sending", o.Key)
		}
		if len(matches) != 0 {
			o.State = FailedBeforeSend
			o.Note = "fixture identity already exists"
			if err = l.Save(*m); err != nil {
				return err
			}
			return fmt.Errorf("preflight %s: %w", o.Key, ErrDuplicate)
		}
		if err = ctx.Err(); err != nil {
			o.State = FailedBeforeSend
			o.Note = "cancelled before sending"
			if saveErr := l.Save(*m); saveErr != nil {
				return saveErr
			}
			return err
		}
		// Persist the uncertain state before the request leaves this process.
		// A crash in the small interval before sending remains conservatively unknown.
		o.State = Unknown
		o.Note = "request may have been sent"
		if len(m.StockChecks) > 0 {
			m.StockMayBeStale = true
		}
		if err = l.Save(*m); err != nil {
			return err
		}
		posts++
		id, err := remote.create(ctx, *o, *m)
		if uuidPattern.MatchString(id) {
			o.RemoteID = id
			if saveErr := l.Save(*m); saveErr != nil {
				return saveErr
			}
		}
		if err != nil || o.RemoteID == "" {
			return fmt.Errorf("create %s: %w", o.Key, ErrUnknownOutcome)
		}
		if o.Kind != "characteristic" {
			matches, err = remote.search(ctx, *o)
			if err != nil || len(matches) != 1 || matches[0] != o.RemoteID {
				return fmt.Errorf("verify identity %s: %w", o.Key, ErrUnknownOutcome)
			}
		}
		if err = remote.verify(ctx, *o, o.RemoteID, *m); err != nil {
			return fmt.Errorf("verify %s: %w", o.Key, ErrUnknownOutcome)
		}
		o.State = Created
		o.Note = ""
		if err = l.Save(*m); err != nil {
			return err
		}
	}
	return nil
}
