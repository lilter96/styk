package catalogsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

// ImportReviewer initializes only an absent, disabled configuration. Existing
// policies and enrollments are never changed by an import.
type ImportReviewer interface {
	CatalogImportContext(context.Context, catalog.Scope, string, []Enrollment) (ReviewContext, error)
}

type ImportRequest struct {
	Scope   catalog.Scope
	Targets []Enrollment
	Actor   string
}

// PreviewImport freezes at most twenty creations in one server plan. Approval
// queues the chosen ready subset atomically through the existing outbox.
func (service Service) PreviewImport(ctx context.Context, request ImportRequest) (Preview, error) {
	if err := service.validateScope(request.Scope); err != nil {
		return Preview{}, err
	}
	store, ok := service.Store.(ImportReviewer)
	if !ok || service.Resolver == nil || !validID(request.Actor) || request.Scope.Validate() != nil || len(request.Targets) == 0 || len(request.Targets) > 20 {
		return Preview{}, ErrInvalid
	}
	targets, err := importTargets(request.Scope, request.Targets)
	if err != nil {
		return Preview{}, err
	}
	current, err := store.CatalogImportContext(ctx, request.Scope, request.Actor, targets)
	if err != nil {
		return Preview{}, fmt.Errorf("load import context: %w", err)
	}
	id, err := newID()
	if err != nil {
		return Preview{}, err
	}
	now := service.now()
	p := Preview{ID: id, Scope: request.Scope, ConfigRevision: current.Config.Revision, Credentials: current.Credentials,
		Actor: request.Actor, CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute), ImportTargets: targets, Decisions: make([]Decision, 0, len(targets))}
	for _, target := range current.Targets {
		decision := service.decide(ctx, current, target, nil)
		if decision.Ready && (decision.Source.Lifecycle != catalog.LifecycleActive || decision.Source.Variant || decision.Source.Parent != nil) {
			decision.block("IMPORT_SOURCE_NOT_ELIGIBLE")
		}
		p.Decisions = append(p.Decisions, decision)
	}
	if err := ctx.Err(); err != nil {
		return Preview{}, fmt.Errorf("prepare import: %w", err)
	}
	return service.Store.SaveCatalogPreview(ctx, p)
}

func importTargets(scope catalog.Scope, input []Enrollment) ([]Enrollment, error) {
	result := make([]Enrollment, 0, len(input))
	seen := make(map[string]bool, len(input))
	for _, e := range input {
		if e.Create == nil || e.Automatic || e.MappingID != "" || !scope.Contains(e.Source.Account) || !scope.Contains(e.Target.Account) || e.Target.ID != "" {
			return nil, ErrInvalid
		}
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s", scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, e.Source.Account.Provider, e.Source.EntityType+":"+e.Source.ID)))
		token := hex.EncodeToString(sum[:16])
		e.ID = "import-" + token
		if seen[e.ID] {
			return nil, ErrInvalid
		}
		seen[e.ID] = true
		creation := *e.Create
		creation.ID = token[:8] + "-" + token[8:12] + "-" + token[12:16] + "-" + token[16:20] + "-" + token[20:]
		creation.ExternalCode = "STYK-IMPORT-" + token
		e.Create, e.AutomaticFields = &creation, nil
		if err := e.validate(scope); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, nil
}

// ValidateImportTargets is also enforced on persisted plans before approval.
func (p Preview) ValidateImportTargets() error {
	if len(p.ImportTargets) == 0 || len(p.ImportTargets) > 20 || p.Automatic {
		return ErrInvalid
	}
	targets, err := importTargets(p.Scope, p.ImportTargets)
	if err != nil {
		return err
	}
	for i, e := range targets {
		previous := p.ImportTargets[i]
		if e.ID != previous.ID || e.Create.ID != previous.Create.ID || e.Create.ExternalCode != previous.Create.ExternalCode {
			return ErrInvalid
		}
	}
	return nil
}
