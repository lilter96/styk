package orders

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"time"
)

func CarriageIDValid(id string) bool {
	value, err := strconv.ParseInt(id, 10, 64)
	return err == nil && value > 0 && strconv.FormatInt(value, 10) == id
}

func (input CarriageInput) Validate() error {
	if !CarriageIDValid(input.DeliveryMethodID) {
		return ErrInvalid
	}
	if input.DepartureDate != "" {
		if _, err := time.Parse(time.RFC3339Nano, input.DepartureDate); err != nil {
			return ErrInvalid
		}
	}
	switch input.Action {
	case CarriageCreate:
		if input.CarriageID != "" || input.AllBLRTraceable == nil || input.ContainersCount != nil || input.TrustAcceptanceConfirmed {
			return ErrInvalid
		}
	case CarriageApprove:
		if !CarriageIDValid(input.CarriageID) || input.AllBLRTraceable != nil || input.DepartureDate != "" {
			return ErrInvalid
		}
		if input.ContainersCount != nil && (*input.ContainersCount < 1 || !input.TrustAcceptanceConfirmed) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func carriageHash(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func CarriageMembershipFingerprint(members []CarriageMember) (string, error) {
	copyMembers := slices.Clone(members)
	for i := range copyMembers {
		if copyMembers[i].Capability != nil {
			profile := *copyMembers[i].Capability
			profile.ObservedAt, profile.ValidUntil = time.Time{}, time.Time{}
			copyMembers[i].Capability = &profile
		}
	}
	slices.SortFunc(copyMembers, func(a, b CarriageMember) int {
		if a.PostingNumber < b.PostingNumber {
			return -1
		}
		if a.PostingNumber > b.PostingNumber {
			return 1
		}
		return 0
	})
	return carriageHash(copyMembers)
}

func CarriageSnapshotFingerprint(snapshot CarriageSnapshot) (string, error) {
	snapshot.Fingerprint = ""
	snapshot.ObservedAt = time.Time{}
	snapshot.Members = slices.Clone(snapshot.Members)
	for i := range snapshot.Members {
		if snapshot.Members[i].Capability != nil {
			profile := *snapshot.Members[i].Capability
			profile.ObservedAt, profile.ValidUntil = time.Time{}, time.Time{}
			snapshot.Members[i].Capability = &profile
		}
	}
	return carriageHash(snapshot)
}

func CarriagePlanFingerprint(plan CarriagePlan) (string, error) {
	plan.Fingerprint = ""
	return carriageHash(plan)
}

func (plan CarriagePlan) Validate() error {
	if !validID(plan.ID) || plan.Scope.Validate() != nil || plan.CarriageInput.Validate() != nil || plan.ConfigRevision < 1 || plan.Credentials.Ozon < 1 || plan.Credentials.Moysklad < 1 {
		return ErrInvalid
	}
	if !CarriageIDValid(plan.WarehouseID) || plan.CreatedAt.IsZero() || !plan.ExpiresAt.After(plan.CreatedAt) || plan.ExpiresAt.Sub(plan.CreatedAt) > 10*time.Minute {
		return ErrInvalid
	}
	if plan.Snapshot.Scope != plan.Scope || plan.Snapshot.DeliveryMethodID != plan.DeliveryMethodID || plan.Snapshot.WarehouseID != plan.WarehouseID || !reflect.DeepEqual(plan.Members, plan.Snapshot.Members) {
		return ErrInvalid
	}
	if len(plan.ReadinessIssues(plan.CreatedAt)) != 0 {
		return ErrBlocked
	}
	source, err := CarriageSnapshotFingerprint(plan.Snapshot)
	if err != nil || source != plan.Snapshot.Fingerprint {
		return ErrInvalid
	}
	membership, err := CarriageMembershipFingerprint(plan.Members)
	if err != nil || membership != plan.MembershipFingerprint {
		return ErrInvalid
	}
	fingerprint, err := CarriagePlanFingerprint(plan)
	if err != nil || fingerprint != plan.Fingerprint {
		return ErrInvalid
	}
	return nil
}

func (plan CarriagePlan) ReadinessIssues(now time.Time) []Validation {
	issues := slices.Clone(plan.Snapshot.Blockers)
	add := func(code string) { issues = append(issues, Validation{Code: code}) }
	if !plan.Snapshot.Complete || plan.Snapshot.ContractVersion == "" {
		add("CARRIAGE_DISCOVERY_INCOMPLETE")
	}
	if plan.Snapshot.ObservedAt.IsZero() || plan.Snapshot.ObservedAt.After(now.Add(time.Second)) || now.Sub(plan.Snapshot.ObservedAt) > 5*time.Minute {
		add("CARRIAGE_SOURCE_STALE")
	}
	if len(plan.Members) == 0 || len(plan.Members) > 1000 {
		add("CARRIAGE_READY_MEMBERS_REQUIRED")
	}
	seen := make(map[string]bool, len(plan.Members))
	for _, member := range plan.Members {
		if !validID(member.PostingNumber) || seen[member.PostingNumber] || !member.Ready || member.Status != "awaiting_deliver" || len(member.Lines) == 0 || len(member.SourceFingerprint) != 64 || len(member.Blockers) != 0 {
			add("CARRIAGE_MEMBER_NOT_READY")
		}
		seen[member.PostingNumber] = true
		if !member.FBSProfileMatches(plan.DeliveryMethodID, plan.WarehouseID, now) {
			add("CARRIAGE_PROFILE_UNVERIFIED")
		}
	}
	if plan.Action == CarriageCreate && len(plan.Snapshot.CandidateIDs) != 0 {
		add("CARRIAGE_OPEN_BATCH_REQUIRES_REVIEW")
	}
	if plan.AllBLRTraceable != nil && *plan.AllBLRTraceable {
		add("CARRIAGE_TRACEABLE_DOCUMENTS_UNVERIFIED")
	}
	if plan.Action == CarriageApprove && (plan.Snapshot.Header == nil || plan.Snapshot.Header.Status != "new" || plan.Snapshot.CarriageID != plan.CarriageID) {
		add("CARRIAGE_APPROVAL_STATE_UNVERIFIED")
	}
	return issues
}

func (member CarriageMember) FBSProfileMatches(method, warehouse string, now time.Time) bool {
	capability := member.Capability
	if member.Scheme != "FBS" || member.DeliveryMethodID != method || member.WarehouseID != warehouse || capability == nil || capability.Validate() != nil {
		return false
	}
	return capability.Scheme == "FBS" && capability.DeliveryMethodID == method && capability.WarehouseID == warehouse && capability.Express.IsKnown() && !*capability.Express.Value && now.Before(capability.ValidUntil) && !now.Before(capability.ObservedAt) && slices.Contains(capability.VerifiedDimensions, "scheme") && slices.Contains(capability.VerifiedDimensions, "warehouse") && slices.Contains(capability.VerifiedDimensions, "express")
}

func (plan CarriagePlan) MatchesMembers(actual CarriageSnapshot) bool {
	if !actual.Complete || actual.Scope != plan.Scope || actual.DeliveryMethodID != plan.DeliveryMethodID || actual.WarehouseID != plan.WarehouseID {
		return false
	}
	return sameCarriageComposition(plan.Members, actual.Members)
}
