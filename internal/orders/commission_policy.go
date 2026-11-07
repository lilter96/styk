package orders

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"
)

func (policy CommissionPolicy) Validate() error {
	if !policy.Enabled {
		return nil
	}
	if !validID(policy.ID) || policy.Scope.Validate() != nil || policy.Revision < 1 || policy.ConfigRevision < 1 || !validID(policy.OwnerID) || policy.GrantedAt.IsZero() {
		return ErrInvalid
	}
	for _, id := range []string{policy.OrganizationID, policy.AgentID, policy.ContractID, policy.CurrencyID} {
		if !validUUID(id) {
			return ErrInvalid
		}
	}
	for _, id := range []string{policy.OrganizationAccountID, policy.AgentAccountID} {
		if id != "" && !validUUID(id) {
			return ErrInvalid
		}
	}
	vat, err := strconv.Atoi(string(policy.VAT))
	if err != nil || strconv.Itoa(vat) != string(policy.VAT) || vat < 0 || vat > 100 || !positiveDecimal(policy.RateValue) {
		return ErrInvalid
	}
	if !knownNativeBoolean(policy.VATEnabled) || !knownNativeBoolean(policy.VATIncluded) || !*policy.VATEnabled.Value && vat != 0 {
		return ErrInvalid
	}
	if *policy.VATEnabled.Value && !*policy.VATIncluded.Value {
		return ErrBlocked
	}
	if policy.PriceBasis != CommissionSellerPrice && policy.PriceBasis != CommissionRealizedPrice || policy.OtherComponents != CommissionZeroOtherComponents && policy.OtherComponents != CommissionPartialReview {
		return ErrInvalid
	}
	if policy.ReturnTreatment != "" && policy.ReturnTreatment != CommissionReturnsRequireZero && policy.ReturnTreatment != CommissionReturnsNative {
		return ErrInvalid
	}
	return (Money{Amount: "0", Currency: policy.CurrencyISO}).Validate()
}

func (policy CommissionPolicy) Active(now time.Time, scope Scope, revision int64) bool {
	return policy.Enabled && policy.Validate() == nil && policy.Scope == scope && policy.ConfigRevision == revision && !now.Before(policy.GrantedAt)
}

func (refs CommissionReferences) Matches(policy CommissionPolicy) bool {
	return refs.Complete && refs.Scope == policy.Scope && refs.OrganizationID == policy.OrganizationID && refs.AgentID == policy.AgentID && refs.ContractID == policy.ContractID && refs.CurrencyID == policy.CurrencyID && refs.CurrencyISO == policy.CurrencyISO && (policy.OrganizationAccountID == "" || refs.OrganizationAccountID == policy.OrganizationAccountID) && (policy.AgentAccountID == "" || refs.AgentAccountID == policy.AgentAccountID) && refs.ContractType == "Commission" && refs.ContractOrganizationID == policy.OrganizationID && refs.ContractAgentID == policy.AgentID && refs.ContractCurrencyID == policy.CurrencyID && refs.ContractRewardType == "None" && validID(refs.Revision) && !refs.ObservedAt.IsZero()
}

func CommissionPlanFingerprint(plan CommissionPlan) (string, error) {
	plan.Fingerprint = ""
	raw, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

func CommissionExternalCode(scope Scope, id string) (string, error) {
	if scope.Validate() != nil || !validID(id) {
		return "", ErrInvalid
	}
	raw, err := json.Marshal(struct {
		Scope Scope
		ID    string
	}{scope, id})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return "styk-commission-v1-" + hex.EncodeToString(hash[:]), nil
}

func (plan CommissionPlan) Validate() error {
	if !validID(plan.ID) || plan.Scope.Validate() != nil || plan.ConfigRevision < 1 || plan.PolicyRevision < 1 || plan.Credentials.Ozon < 1 || plan.Credentials.Moysklad < 1 || plan.Policy.Validate() != nil || !plan.Policy.Enabled || plan.Policy.Scope != plan.Scope || plan.Policy.ConfigRevision != plan.ConfigRevision || plan.Policy.Revision != plan.PolicyRevision || !plan.References.Matches(plan.Policy) {
		return ErrInvalid
	}
	if plan.Source.Scope != plan.Scope || plan.Source.Currency != plan.Policy.CurrencyISO || len(plan.Source.Fingerprint) != 64 || len(plan.Lines)+len(plan.Returns) == 0 || len(plan.Lines)+len(plan.Returns) > MaxCommissionLines || plan.CreatedAt.IsZero() || !plan.ExpiresAt.After(plan.CreatedAt) || plan.ExpiresAt.Sub(plan.CreatedAt) > 15*time.Minute {
		return ErrInvalid
	}
	code, err := CommissionExternalCode(plan.Scope, plan.ID)
	if err != nil || code != plan.ExternalCode {
		return ErrInvalid
	}
	if plan.validateAccounting() != nil || plan.validateLines(plan.Lines) != nil || plan.validateLines(plan.Returns) != nil {
		return ErrInvalid
	}
	fingerprint, err := CommissionPlanFingerprint(plan)
	if err != nil || fingerprint != plan.Fingerprint {
		return ErrInvalid
	}
	return nil
}

func (plan CommissionPlan) validateLines(lines []CommissionLine) error {
	seen := make(map[int64]bool, len(lines))
	for _, line := range lines {
		if seen[line.SourceRow] || line.Mapping.Validate() != nil || line.Mapping.Scope != plan.Scope || !positiveDecimal(line.Quantity) || line.Price.Validate() != nil || !nonnegativeDecimal(line.Price.Amount) || line.Reward.Validate() != nil || line.Price.Currency != plan.Policy.CurrencyISO || line.Reward.Currency != plan.Policy.CurrencyISO {
			return ErrInvalid
		}
		seen[line.SourceRow] = true
	}
	return nil
}

func (plan CommissionPlan) validateAccounting() error {
	if len(plan.Returns) > 0 && (plan.Policy.ReturnTreatment != CommissionReturnsNative || plan.ReturnTotals == nil || plan.Accounting == nil) {
		return ErrInvalid
	}
	if plan.Accounting == nil {
		return nil
	}
	ledger := plan.Accounting
	if ledger.SourceFingerprint != plan.Source.Fingerprint || ledger.Period != plan.Source.Period || ledger.Currency != plan.Policy.CurrencyISO || ledger.RowsTotal != len(plan.Source.Rows) || ledger.Validate() != nil {
		return ErrInvalid
	}
	if !ledger.Complete && plan.Policy.OtherComponents != CommissionPartialReview {
		return ErrInvalid
	}
	return nil
}

func (plan CommissionPlan) PeriodStart() string {
	return time.Date(plan.Source.Period.Year, time.Month(plan.Source.Period.Month), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02 15:04:05.000")
}

func (plan CommissionPlan) PeriodEnd() string {
	return time.Date(plan.Source.Period.Year, time.Month(plan.Source.Period.Month)+1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Millisecond).Format("2006-01-02 15:04:05.000")
}

func (plan CommissionPlan) Moment() string {
	if !plan.Source.DocumentDate.IsKnown() {
		return ""
	}
	date, err := time.Parse("2006-01-02", *plan.Source.DocumentDate.Value)
	if err != nil {
		return ""
	}
	return date.Format("2006-01-02 15:04:05.000")
}
