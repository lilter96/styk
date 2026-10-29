package application

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

// EvidenceWindow is a frozen half-open time interval. Completed slices are
// traversals, not proof of provider visibility or a point-in-time snapshot.
type EvidenceWindow struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type EvidenceSlice[T any] struct {
	Window EvidenceWindow `json:"window"`
	Value  T              `json:"value"`
}

type WindowProgress[T any] struct {
	Completed []EvidenceSlice[T] `json:"completed"`
	Pending   []EvidenceWindow   `json:"pending"`
}

const maxEvidenceSlices = 4096

func NewWindowProgress[T any](from, to time.Time) WindowProgress[T] {
	return WindowProgress[T]{Completed: []EvidenceSlice[T]{}, Pending: []EvidenceWindow{{From: from, To: to}}}
}

// Valid proves there are no gaps or overlaps, including after a restart.
func (p WindowProgress[T]) Valid(from, to time.Time) bool {
	if from.IsZero() || !to.After(from) || len(p.Completed)+len(p.Pending) > maxEvidenceSlices {
		return false
	}
	next := from
	for _, s := range p.Completed {
		if !validEvidenceWindow(s.Window, next) {
			return false
		}
		next = s.Window.To
	}
	for _, w := range p.Pending {
		if !validEvidenceWindow(w, next) {
			return false
		}
		next = w.To
	}
	return next.Equal(to)
}

func validEvidenceWindow(w EvidenceWindow, from time.Time) bool {
	return w.From.Equal(from) && w.To.After(w.From) && w.From.Equal(w.From.Truncate(time.Millisecond)) && w.To.Equal(w.To.Truncate(time.Millisecond))
}

// Split leaves already completed windows untouched. A single millisecond is
// the smallest safely representable MoySklad filter interval.
func (p *WindowProgress[T]) Split() bool {
	if len(p.Pending) == 0 || len(p.Completed)+len(p.Pending) >= maxEvidenceSlices {
		return false
	}
	w := p.Pending[0]
	middle := w.From.Add(w.To.Sub(w.From) / 2).Truncate(time.Millisecond)
	if !middle.After(w.From) || !middle.Before(w.To) {
		return false
	}
	pending := make([]EvidenceWindow, 0, len(p.Pending)+1)
	pending = append(pending, EvidenceWindow{w.From, middle}, EvidenceWindow{middle, w.To})
	p.Pending = append(pending, p.Pending[1:]...)
	return true
}

func (p *WindowProgress[T]) Add(value T) error {
	if len(p.Pending) == 0 {
		return errors.New("no pending evidence window")
	}
	p.Completed = append(p.Completed, EvidenceSlice[T]{Window: p.Pending[0], Value: value})
	p.Pending = p.Pending[1:]
	return nil
}

var ErrEvidenceConflict = errors.New("repeated evidence changed during traversal")

func CombineCustomerOrderSlices(p WindowProgress[domain.CustomerOrderImport], tenant, account string, from, to, started, finished time.Time) (domain.CustomerOrderImport, error) {
	out := domain.CustomerOrderImport{Tenant: tenant, Account: account, Since: from, Until: to, StartedAt: started, FinishedAt: finished}
	if !p.Valid(from, to) || len(p.Pending) != 0 {
		return domain.CustomerOrderImport{}, errors.New("customer order window is incomplete")
	}
	seen := make(map[string]domain.CustomerOrderObservation)
	for _, slice := range p.Completed {
		b := slice.Value
		if b.Tenant != tenant || b.Account != account || !b.Since.Equal(slice.Window.From) || !b.Until.Equal(slice.Window.To) || b.StartedAt.IsZero() || b.FinishedAt.Before(b.StartedAt) {
			return domain.CustomerOrderImport{}, errors.New("customer order slice scope is inconsistent")
		}
		for _, o := range b.Orders {
			key := strings.ToLower(o.ID)
			if o.ID == "" || o.Account != account {
				return domain.CustomerOrderImport{}, errors.New("customer order identity is inconsistent")
			}
			if prior, ok := seen[key]; ok && !sameCustomerOrder(prior, o) {
				return domain.CustomerOrderImport{}, ErrEvidenceConflict
			}
			if prior, ok := seen[key]; !ok || o.ObservedAt.After(prior.ObservedAt) {
				seen[key] = o
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out.Orders = append(out.Orders, seen[key])
	}
	return out, nil
}

func sameCustomerOrder(a, b domain.CustomerOrderObservation) bool {
	normalize := func(o domain.CustomerOrderObservation) domain.CustomerOrderObservation {
		o.ObservedAt = time.Time{}
		o.Positions = append([]domain.CustomerOrderPosition{}, o.Positions...)
		for i := range o.Positions {
			o.Positions[i].ObservedAt = time.Time{}
		}
		sort.Slice(o.Positions, func(i, j int) bool { return o.Positions[i].ID < o.Positions[j].ID })
		return o
	}
	return reflect.DeepEqual(normalize(a), normalize(b))
}

func CombinePostingSlices(p WindowProgress[domain.PostingImport], tenant, account string, from, to, started, finished time.Time) (domain.PostingImport, error) {
	out := domain.PostingImport{Tenant: tenant, Account: account, Since: from, Until: to, StartedAt: started, FinishedAt: finished}
	if !p.Valid(from, to) || len(p.Pending) != 0 {
		return domain.PostingImport{}, errors.New("posting window is incomplete")
	}
	seen := make(map[string]domain.PostingObservation)
	for _, slice := range p.Completed {
		b := slice.Value
		if b.Tenant != tenant || b.Account != account || !b.Since.Equal(slice.Window.From) || !b.Until.Equal(slice.Window.To) || b.StartedAt.IsZero() || b.FinishedAt.Before(b.StartedAt) {
			return domain.PostingImport{}, errors.New("posting slice scope is inconsistent")
		}
		for _, o := range b.Postings {
			if o.PostingNumber == "" {
				return domain.PostingImport{}, errors.New("posting identity is missing")
			}
			if prior, ok := seen[o.PostingNumber]; ok && !samePosting(prior, o) {
				return domain.PostingImport{}, ErrEvidenceConflict
			}
			if prior, ok := seen[o.PostingNumber]; !ok || o.ObservedAt.After(prior.ObservedAt) {
				seen[o.PostingNumber] = o
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out.Postings = append(out.Postings, seen[key])
	}
	return out, nil
}

func samePosting(a, b domain.PostingObservation) bool {
	normalize := func(o domain.PostingObservation) domain.PostingObservation {
		o.ObservedAt = time.Time{}
		o.Lines = append([]domain.PostingLine{}, o.Lines...)
		sort.Slice(o.Lines, func(i, j int) bool {
			a, b := o.Lines[i], o.Lines[j]
			if a.OfferID != b.OfferID {
				return a.OfferID < b.OfferID
			}
			if a.SKU != b.SKU {
				return a.SKU < b.SKU
			}
			return a.Quantity < b.Quantity
		})
		return o
	}
	return reflect.DeepEqual(normalize(a), normalize(b))
}
