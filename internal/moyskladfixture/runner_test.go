package moyskladfixture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const testAccount = "10000000-0000-0000-0000-000000000000"
const testNamespace = "STYK-IT-20261029-012345abcdef"

type fakeRemote struct {
	lock                *Lock
	items               map[string]string
	posts               int
	loseResponse        bool
	verificationFails   bool
	verificationFailKey string
	prepareFails        bool
	coverageUnknown     bool
}

func (f *fakeRemote) preflight(context.Context, string) (map[string]int, error) {
	return map[string]int{"store": 1}, nil
}
func (f *fakeRemote) checkNamespace(context.Context, Manifest) error {
	if len(f.items) > 0 {
		return ErrDuplicate
	}
	return nil
}

func (f *fakeRemote) prepare(context.Context, Operation, Manifest) error {
	if f.prepareFails {
		return errors.New("synthetic failure")
	}
	return nil
}
func (f *fakeRemote) search(_ context.Context, o Operation) ([]string, error) {
	if id := f.items[o.Key]; id != "" {
		if o.Kind == "characteristic" && f.coverageUnknown {
			return []string{id}, ErrCoverageUnknown
		}
		return []string{id}, nil
	}
	if o.Kind == "characteristic" && f.coverageUnknown {
		return nil, ErrCoverageUnknown
	}
	return nil, nil
}
func (f *fakeRemote) create(_ context.Context, o Operation, _ Manifest) (string, error) {
	durable, err := f.lock.Load()
	if err != nil {
		return "", err
	}
	for _, v := range durable.Operations {
		if v.Key == o.Key && v.State != Unknown {
			return "", errors.New("POST preceded durable unknown state")
		}
	}
	f.posts++
	id := fmt.Sprintf("20000000-0000-0000-0000-%012d", f.posts)
	f.items[o.Key] = id
	if f.loseResponse {
		f.loseResponse = false
		return "", errors.New("synthetic lost response")
	}
	return id, nil
}
func (f *fakeRemote) verify(_ context.Context, o Operation, id string, _ Manifest) error {
	if f.verificationFails || f.verificationFailKey == o.Key || f.items[o.Key] != id {
		return errors.New("mismatch")
	}
	return nil
}

func fixture(t *testing.T) (*Lock, Manifest, *fakeRemote) {
	t.Helper()
	l, err := OpenLock(filepath.Join(t.TempDir(), "private"), testNamespace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Error(err)
		}
	})
	m, err := l.Create(testAccount)
	if err != nil {
		t.Fatal(err)
	}
	return l, m, &fakeRemote{lock: l, items: map[string]string{}}
}

func TestUnknownOutcomeIsDurableAndReconciledWithoutRetry(t *testing.T) {
	l, m, r := fixture(t)
	r.loseResponse = true
	if err := Run(context.Background(), l, &m, testAccount, r, 1); !errors.Is(err, ErrUnknownOutcome) {
		t.Fatalf("expected unknown outcome: %v", err)
	}
	persisted, err := l.Load()
	if err != nil || persisted.Operations[0].State != Unknown || persisted.Operations[0].RemoteID != "" {
		t.Fatal("unknown result was not durable")
	}
	if err = Run(context.Background(), l, &persisted, testAccount, r, 1); !errors.Is(err, ErrOperationLimit) {
		t.Fatalf("expected bounded continuation: %v", err)
	}
	if persisted.Operations[0].State != Created || r.posts != 2 {
		t.Fatalf("resume retried uncertain request or stopped early: %+v", persisted.Operations[0])
	}
}

func TestUnknownAbsenceAndDuplicateNeverCausePost(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing bool
	}{{"unknown and absent", false}, {"planned and duplicate", true}} {
		t.Run(tc.name, func(t *testing.T) {
			l, m, r := fixture(t)
			if tc.existing {
				r.items[m.Operations[0].Key] = "20000000-0000-0000-0000-000000000001"
			} else {
				m.Operations[0].State = Unknown
				m.NamespaceChecked = true
				if err := l.Save(m); err != nil {
					t.Fatal(err)
				}
			}
			err := Run(context.Background(), l, &m, testAccount, r, 1)
			if tc.existing && !errors.Is(err, ErrDuplicate) {
				t.Fatal(err)
			}
			if !tc.existing && !errors.Is(err, ErrUnknownOutcome) {
				t.Fatal(err)
			}
			if r.posts != 0 {
				t.Fatal("unsafe create")
			}
		})
	}
}

func TestRepeatedExecutionAndPartialResume(t *testing.T) {
	l, m, r := fixture(t)
	if err := Run(context.Background(), l, &m, testAccount, r, 1); !errors.Is(err, ErrOperationLimit) {
		t.Fatal(err)
	}
	if err := Run(context.Background(), l, &m, testAccount, r, 1); !errors.Is(err, ErrOperationLimit) {
		t.Fatal(err)
	}
	if r.posts != 2 || m.Operations[0].State != Created || m.Operations[1].State != Created {
		t.Fatal("partial resume duplicated fixture")
	}
}

func TestWrongAccountCancellationAndVerificationFailure(t *testing.T) {
	l, m, r := fixture(t)
	if err := Run(context.Background(), l, &m, "10000000-0000-0000-0000-000000000001", r, 1); err == nil || r.posts != 0 {
		t.Fatal("wrong account accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, l, &m, testAccount, r, 1); !errors.Is(err, context.Canceled) || m.Operations[0].State != FailedBeforeSend || r.posts != 0 {
		t.Fatal("cancelled request was sent")
	}
	r.verificationFails = true
	if err := Run(context.Background(), l, &m, testAccount, r, 1); !errors.Is(err, ErrUnknownOutcome) || m.Operations[0].State != Unknown || m.Operations[0].RemoteID == "" {
		t.Fatal("verification failure treated as success")
	}
}

func TestManifestNamespaceLockAndPermissions(t *testing.T) {
	l, m, _ := fixture(t)
	if _, err := l.Create(testAccount); err == nil {
		t.Fatal("repeated namespace overwrote manifest")
	}
	if second, err := OpenLock(l.directory, testNamespace); err == nil {
		if closeErr := second.Close(); closeErr != nil {
			t.Errorf("close unexpected namespace lock: %v", closeErr)
		}
		t.Fatal("concurrent namespace lock succeeded")
	}
	info, err := os.Stat(l.path())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("manifest permissions")
	}
	m.Operations[0].Kind = "payment"
	if err = l.Save(m); err == nil {
		t.Fatal("modified plan accepted")
	}
}

func TestOnlyNeverSentSkippedShipmentCanBeEnabled(t *testing.T) {
	l, m, _ := fixture(t)
	m.NamespaceChecked = true
	for i := range m.Operations {
		o := &m.Operations[i]
		switch o.Key {
		case "warehouse-a", "product-b", "counterparty", "stock-a", "order-partial":
			o.State = Created
			o.RemoteID = fmt.Sprintf("20000000-0000-0000-0000-%012d", i+1)
		case "shipment-partial":
			o.State = Skipped
		}
	}
	if err := l.Save(m); err != nil {
		t.Fatal(err)
	}
	if err := EnableSkippedShipment(l, &m, testAccount); err != nil {
		t.Fatal(err)
	}
	for i := range m.Operations {
		if m.Operations[i].Key == "shipment-partial" {
			if m.Operations[i].State != Planned {
				t.Fatal("shipment was not replanned")
			}
			m.Operations[i].State = Unknown
		}
	}
	if err := l.Save(m); err != nil {
		t.Fatal(err)
	}
	if err := EnableSkippedShipment(l, &m, testAccount); err == nil {
		t.Fatal("unknown shipment was made retryable")
	}
}

func TestMultipleOrderFallbackRequiresSafelySkippedVariant(t *testing.T) {
	l, m, _ := fixture(t)
	m.NamespaceChecked = true
	for i := range m.Operations {
		o := &m.Operations[i]
		switch o.Key {
		case "warehouse-a", "product-a", "product-c", "counterparty":
			o.State = Created
			o.RemoteID = fmt.Sprintf("20000000-0000-0000-0000-%012d", i+1)
		case "variant-one", "order-multiple":
			o.State = Skipped
		}
	}
	if err := l.Save(m); err != nil {
		t.Fatal(err)
	}
	if err := EnableSkippedMultipleOrder(l, &m, testAccount); err != nil {
		t.Fatal(err)
	}
	c := mockClient(t, &fakeTransport{items: map[string]map[string]any{}})
	c.organization = "30000000-0000-0000-0000-000000000001"
	for _, o := range m.Operations {
		if o.Key == "order-multiple" {
			body, err := c.payload(o, m)
			if err != nil || len(body["positions"].([]any)) != 2 {
				t.Fatal("two-product fallback was not constructed")
			}
		}
	}
	for i := range m.Operations {
		if m.Operations[i].Key == "order-multiple" {
			m.Operations[i].State = Unknown
		}
	}
	if err := l.Save(m); err != nil {
		t.Fatal(err)
	}
	if err := EnableSkippedMultipleOrder(l, &m, testAccount); err == nil {
		t.Fatal("unknown order was made retryable")
	}
}
