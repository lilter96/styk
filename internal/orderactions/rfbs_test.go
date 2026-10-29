package orderactions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestRFBSPlanBindsIntentWithoutCreatingRequest(t *testing.T) {
	t.Parallel()
	posting := rfbsTestPosting()
	command := rfbsTestCommand(t, posting, orders.ActionPostingShip)
	before, err := orders.CommandFingerprint(command)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanRFBSAt(posting, command, rfbsTestNow)
	if !errors.Is(err, orders.ErrBlocked) || plan.Allowed || len(plan.Body) != 0 || plan.Endpoint != "/v2/fbs/posting/delivering" || len(plan.ContractHash) != 64 {
		t.Fatalf("unverified request became executable: %+v error=%v", plan, err)
	}
	if plan.SourceRevision != posting.Evidence.Revision || plan.CommandID != command.ID || !rfbsHasDiagnostic(plan.Diagnostics, "rfbs_request_contract_unverified") {
		t.Fatal("source or contract binding missing")
	}
	after, err := orders.CommandFingerprint(command)
	if err != nil || before != after {
		t.Fatal("inspection mutated the durable command")
	}
	raw, err := json.Marshal(plan)
	if err != nil || len(raw) == 0 {
		t.Fatal("plan cannot be reviewed")
	}
}

func TestRFBSPlanRejectsChangedReviewedFacts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*orders.Command)
	}{
		{"source revision", func(c *orders.Command) { c.ExpectedSourceRevision = "source-r2" }},
		{"delivery method", func(c *orders.Command) { c.PostingAction.Capability.DeliveryMethodID = "different-method" }},
		{"carrier facts with same revision", func(c *orders.Command) { c.PostingAction.Capability.Carrier = orders.Present("integrated") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			posting := rfbsTestPosting()
			command := rfbsTestCommand(t, posting, orders.ActionPostingShip)
			tc.mutate(&command)
			plan, err := PlanRFBSAt(posting, command, rfbsTestNow)
			if !errors.Is(err, orders.ErrStale) || plan.Allowed || len(plan.Body) != 0 {
				t.Fatalf("changed reviewed facts accepted: allowed=%v error=%v", plan.Allowed, err)
			}
		})
	}
}

func TestRFBSHybridNoticeEvidenceIsCanonicalAndExplicitlyScoped(t *testing.T) {
	t.Parallel()
	source := RFBSHybridSource()
	sum := sha256.Sum256([]byte(source.Evidence))
	if source.SHA256 != hex.EncodeToString(sum[:]) || source.URL != RFBSHybridNoticeURL || source.Status != "official_notice_verified" {
		t.Fatal("verified notice evidence lost")
	}
	if source.HashBasis != "evidence_summary_utf8" || source.CheckedAt != "2026-10-30" || source.SHA256 != "8e551c51b100c40376cc51702c15ae77f2956c18dfeaac82e2177e26a61f20dd" {
		t.Fatal("notice summary hash was confused with a complete API schema")
	}
}
