package orders

import (
	"fmt"
	"time"
)

// ObserveMerchantTransition compares two complete exact point reads. Polling can
// miss intermediate changes; no account snapshot or exactly-once HTTP is claimed.
func ObserveMerchantTransition(before, after NativeOrderPoint, config Config, own NativeStateBaseline, scheme string, now time.Time) (MerchantIntent, bool) {
	intents := ObserveMerchantTransitions(before, after, config, own, scheme, now)
	if len(intents) == 0 {
		return MerchantIntent{}, false
	}
	return intents[0], true
}

func ObserveMerchantTransitions(before, after NativeOrderPoint, config Config, own NativeStateBaseline, scheme string, now time.Time) []MerchantIntent {
	policy := config.Lifecycle
	if policy == nil || !policy.Active(now, config.Scope, config.Revision) || before.Validate() != nil || after.Validate() != nil || before.Scope != config.Scope || before.Correlation != after.Correlation || after.CredentialRevision != before.CredentialRevision || before.StateID == after.StateID || !before.ProviderUpdatedAt.IsKnown() || !after.ProviderUpdatedAt.IsKnown() {
		return nil
	}
	if own.Validate() != nil || own.Correlation != after.Correlation || own.StateID == after.StateID || before.SourceDeleted.IsKnown() || after.SourceDeleted.IsKnown() || !after.StartedAt.After(before.FinishedAt) || now.Sub(before.FinishedAt) > 15*time.Minute || now.Before(after.FinishedAt) {
		return nil
	}
	intents := make([]MerchantIntent, 0, 2)
	for _, mapping := range policy.Mappings {
		if mapping.Scheme != scheme {
			continue
		}
		intent, err := transitionIntent(before, after, config, mapping)
		if err != nil {
			return nil
		}
		if _, found := policy.Mapping(intent); found && intent.Validate() == nil {
			intents = append(intents, intent)
		}
	}
	return intents
}

func transitionIntent(before, after NativeOrderPoint, config Config, mapping MerchantCommandMapping) (MerchantIntent, error) {
	intent := MerchantIntent{Scope: after.Scope, Correlation: after.Correlation, BeforeStateID: before.StateID, AfterStateID: after.StateID, BeforeRevision: before.TargetRevision, AfterRevision: after.TargetRevision, BeforeUpdatedAt: *before.ProviderUpdatedAt.Value, AfterUpdatedAt: *after.ProviderUpdatedAt.Value, ObservedAt: after.FinishedAt, ConfigRevision: config.Revision, CredentialRevision: after.CredentialRevision, Action: mapping.Action, Scheme: mapping.Scheme, EventKind: mapping.EventKind, WholePosting: mapping.WholePosting}
	var err error
	intent.Digest, err = intent.Fingerprint()
	if err != nil {
		return MerchantIntent{}, err
	}
	hash := intent.Digest
	intent.ID = fmt.Sprintf("%s-%s-5%s-a%s-%s", hash[:8], hash[8:12], hash[13:16], hash[17:20], hash[20:32])
	return intent, nil
}
