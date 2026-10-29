package catalogsync

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

func (service Service) preview(ctx context.Context, request PreviewRequest, automatic bool) (Preview, error) {
	current, err := service.reviewContext(ctx, request)
	if err != nil {
		return Preview{}, err
	}
	selected, err := selectTargets(current.Targets, request.EnrollmentIDs)
	if err != nil {
		return Preview{}, err
	}
	id, err := newID()
	if err != nil {
		return Preview{}, err
	}
	now := service.now()
	preview := Preview{ID: id, Scope: request.Scope, ConfigRevision: current.Config.Revision, Credentials: current.Credentials,
		Actor: request.Actor, Automatic: automatic, CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute), Decisions: make([]Decision, 0, len(selected))}
	for _, target := range selected {
		decision := service.decide(ctx, current, target, request.ManualValues[target.Enrollment.ID])
		if automatic && !target.Enrollment.authorizes(decision) {
			decision.block("AUTOMATIC_FIELDS_NOT_AUTHORIZED")
		}
		preview.Decisions = append(preview.Decisions, decision)
	}
	if err := ctx.Err(); err != nil {
		return Preview{}, fmt.Errorf("build catalog preview: %w", err)
	}
	saved, err := service.Store.SaveCatalogPreview(ctx, preview)
	if err != nil {
		return Preview{}, fmt.Errorf("persist frozen catalog preview: %w", err)
	}
	return saved, nil
}

func (service Service) reviewContext(ctx context.Context, request PreviewRequest) (ReviewContext, error) {
	if err := service.validateScope(request.Scope); err != nil {
		return ReviewContext{}, err
	}
	if !validID(request.Actor) || request.ExpectedRevision < 1 || service.Resolver == nil {
		return ReviewContext{}, ErrInvalid
	}
	current, err := service.Store.CatalogReviewContext(ctx, request.Scope)
	if err != nil {
		return ReviewContext{}, fmt.Errorf("load catalog review evidence: %w", err)
	}
	if current.Config.Scope != request.Scope || current.Config.Revision != request.ExpectedRevision {
		return ReviewContext{}, ErrStale
	}
	if err := current.Config.Validate(); err != nil {
		return ReviewContext{}, fmt.Errorf("validate persisted catalog configuration: %w", err)
	}
	if len(current.Targets) != len(current.Config.Targets) {
		return ReviewContext{}, ErrStale
	}
	return current, nil
}

func selectTargets(targets []TargetContext, ids []string) ([]TargetContext, error) {
	if len(ids) == 0 {
		return targets, nil
	}
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !validID(id) || selected[id] {
			return nil, ErrInvalid
		}
		selected[id] = true
	}
	result := make([]TargetContext, 0, len(ids))
	for _, target := range targets {
		if selected[target.Enrollment.ID] {
			result = append(result, target)
			delete(selected, target.Enrollment.ID)
		}
	}
	if len(selected) != 0 {
		return nil, ErrNotFound
	}
	return result, nil
}

func (service Service) decide(ctx context.Context, current ReviewContext, target TargetContext, manual map[catalog.Field]catalog.Value) Decision {
	decision := Decision{EnrollmentID: target.Enrollment.ID, Mapping: target.Mapping, Baseline: target.Baseline,
		Commands: make([]catalog.Command, 0, 2), Validation: make([]catalog.FieldError, 0)}
	if target.Fenced {
		decision.block("TARGET_OUTCOME_UNRESOLVED")
		return decision
	}
	if target.CreatedTarget != nil && target.Enrollment.Create != nil {
		decision.block("CREATED_TARGET_REQUIRES_MAPPING")
		return decision
	}
	if target.Enrollment.Create == nil && !mappingMatches(target) {
		decision.block("CONFIRMED_MAPPING_CHANGED")
		return decision
	}
	if !service.observeDecision(ctx, current, target, &decision) {
		return decision
	}
	if target.Enrollment.Create != nil {
		return creationDecision(current.Config, target.Enrollment, decision)
	}
	return updateDecision(current.Config, decision, manual)
}

func mappingMatches(target TargetContext) bool {
	mapping := target.Mapping
	enrollment := target.Enrollment
	return mapping != nil && mapping.Confirmed && mapping.ID == enrollment.MappingID && mapping.Version == enrollment.MappingVersion &&
		mapping.Source.SameEntity(enrollment.Source) && mapping.Target.SameEntity(enrollment.Target)
}

func (service Service) observeDecision(ctx context.Context, current ReviewContext, target TargetContext, decision *Decision) bool {
	scope := current.Config.Scope
	source, err := resolveProvider(ctx, service.Resolver, scope, target.Enrollment.Source.Account, current.Credentials)
	if err != nil {
		decision.block("SOURCE_CREDENTIALS_UNAVAILABLE")
		return false
	}
	decision.Source, err = readProduct(ctx, source, scope, target.Enrollment.Source)
	if err != nil {
		decision.block("SOURCE_READ_UNAVAILABLE")
		return false
	}
	destination, err := resolveProvider(ctx, service.Resolver, scope, target.Enrollment.Target.Account, current.Credentials)
	if err != nil {
		decision.block("TARGET_CREDENTIALS_UNAVAILABLE")
		return false
	}
	return observeDestination(ctx, destination, scope, target, decision)
}

func observeDestination(ctx context.Context, destination catalog.Reader, scope catalog.Scope, target TargetContext, decision *Decision) bool {
	if target.Enrollment.Create != nil {
		if target.Mapping != nil && target.Mapping.Confirmed {
			decision.block("CREATE_HAS_CONFIRMED_MAPPING")
			return false
		}
		snapshot, err := readCreationTarget(ctx, destination, scope, target.Enrollment)
		if err != nil || len(snapshot.Products) != 0 {
			decision.block("CREATE_TARGET_NOT_PROVEN_ABSENT")
			return false
		}
		decision.Target = catalog.Product{Identity: target.Enrollment.Target}
		decision.TargetAbsent = true
		return true
	}
	product, err := readProduct(ctx, destination, scope, target.Enrollment.Target)
	if err != nil {
		decision.block("TARGET_READ_UNAVAILABLE")
		return false
	}
	decision.Target = product
	return true
}

func updateDecision(config Config, decision Decision, manual map[catalog.Field]catalog.Value) Decision {
	plan, err := catalog.BuildPlan(catalog.PlanRequest{Mapping: *decision.Mapping, Source: decision.Source, Target: decision.Target,
		Policies: config.Rules, Baseline: decision.Baseline, ManualValues: manual})
	if err != nil {
		decision.block("SELECTED_VALUES_INVALID")
		return decision
	}
	decision.Plan = plan
	for _, skipped := range plan.Skipped {
		if skipped.Reason != "ignored" {
			decision.Validation = append(decision.Validation, catalog.FieldError{Field: skipped.Field, Code: skipped.Reason})
		}
	}
	if len(plan.Conflicts) != 0 || len(decision.Validation) != 0 {
		return decision
	}
	commands, err := updateCommands(config, decision)
	if err != nil {
		decision.block("COMMAND_INVALID")
		return decision
	}
	decision.Commands, decision.Ready = commands, true
	return decision
}

func updateCommands(config Config, decision Decision) ([]catalog.Command, error) {
	grouped := make(map[catalog.Provider]catalog.Command, 2)
	for _, change := range decision.Plan.Changes {
		command, ok := grouped[change.Target.Account.Provider]
		if !ok {
			var err error
			command, err = commandForChange(config, decision, change)
			if err != nil {
				return nil, err
			}
		}
		command.Values[change.Field] = change.After
		grouped[change.Target.Account.Provider] = command
	}
	commands := make([]catalog.Command, 0, len(grouped))
	for _, command := range grouped {
		if err := catalog.ValidateCommand(command); err != nil {
			return nil, err
		}
		commands = append(commands, command)
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Target.Account.Provider < commands[j].Target.Account.Provider })
	return commands, nil
}

func commandForChange(config Config, decision Decision, change catalog.Change) (catalog.Command, error) {
	id, err := newID()
	if err != nil {
		return catalog.Command{}, err
	}
	source, target := decision.Source, decision.Target
	if source.Identity.Account != change.Source.Account {
		source, target = target, source
	}
	return catalog.Command{ID: id, Scope: config.Scope, MappingID: decision.Mapping.ID, MappingVersion: decision.Mapping.Version,
		PolicyRevision: strconv.FormatInt(config.Revision, 10), Source: change.Source, Target: change.Target, Action: catalog.ActionUpdate,
		Values: make(map[catalog.Field]catalog.Value), ExpectedSourceRevision: source.Revision, ExpectedTargetRevision: target.Revision,
		IdempotencyKey: id}, nil
}

func creationDecision(config Config, enrollment Enrollment, decision Decision) Decision {
	id, err := newID()
	if err != nil {
		decision.block("COMMAND_ID_UNAVAILABLE")
		return decision
	}
	create := enrollment.Create
	command := catalog.Command{ID: id, Scope: config.Scope, Source: enrollment.Source, Target: enrollment.Target, Action: catalog.ActionCreate,
		Values: create.Values, Extensions: create.Extensions, Parent: create.Parent, CreateID: create.ID, CreateExternalCode: create.ExternalCode,
		IdempotencyKey: id, PolicyRevision: strconv.FormatInt(config.Revision, 10), ExpectedSourceRevision: decision.Source.Revision}
	if err := catalog.ValidateCommand(command); err != nil {
		decision.block("CREATE_VALUES_INVALID")
		return decision
	}
	decision.Commands = []catalog.Command{command}
	decision.Ready = true
	return decision
}
