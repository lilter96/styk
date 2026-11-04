package orders

import (
	"errors"
	"testing"
)

func retainedSplitFixture(t *testing.T) (Posting, CustomerOrderWrite, PositionBaseline, SplitRetainedReservePermission) {
	t.Helper()
	command, baseline := positionPatchFixture(t)
	binding := &baseline.Bindings[0]
	binding.Source.Quantity, binding.Native.Quantity = "4", "4"
	binding.Source.Reserve, binding.Native.Reserve = Present(Decimal("4")), Present(Decimal("4"))
	before := testPosting()
	before.Lines = before.Lines[:1]
	before.Lines[0].Quantity = "4"
	current := before
	current.Lines = append([]PostingLine(nil), before.Lines...)
	current.Lines[0].Quantity = "2"
	current.ChildPostingNumbers = []string{"actual-child"}
	current.Evidence.Revision = "actual-after-split"
	write := *command.CustomerOrder
	write.Positions = append([]PositionWrite(nil), write.Positions...)
	write.Positions[0].Quantity = "2"
	write.Positions[0].Reserve = Field[Decimal]{}
	write.Applicable = Present(true)
	permission := SplitRetainedReservePermission{Scope: current.Scope, SplitWorkID: "reconciled-split-work", Before: before, Correlation: baseline.Correlation}
	return current, write, baseline, permission
}

func TestRetainedSplitReserveReductionUsesExistingOwnedPositionPatch(t *testing.T) {
	t.Parallel()
	posting, write, baseline, proof := retainedSplitFixture(t)
	if invalid := ApplyRetainedSplitOpenQuantityReserve(posting, &write, baseline, proof); invalid.Code != "" {
		t.Fatalf("owned parent reduction blocked: %+v", invalid)
	}
	if !ExactDecimalEqual(*write.Positions[0].Reserve.Value, "2") {
		t.Fatal("retained parent reserve was not reduced exactly")
	}
	command := testCommand(t)
	command.Correlation, command.CustomerOrder = baseline.Correlation, &write
	command.TargetPositions = []TargetPosition{baseline.Bindings[0].Native, {ID: "foreign-position", MoyskladKind: "product", MoyskladID: "foreign-product", Quantity: "7", Reserve: Present(Decimal("2"))}}
	command.TargetPositionsComplete = true
	patch, err := PlanPositionPatch(command, baseline)
	if err != nil || patch == nil || patch.PositionID != baseline.Bindings[0].Native.ID || !patch.Quantity.IsKnown() || *patch.Quantity.Value != "2" || !patch.Reserve.IsKnown() || *patch.Reserve.Value != "2" {
		t.Fatalf("existing exact native patch unavailable: %+v %v", patch, err)
	}
	if *command.TargetPositions[1].Reserve.Value != "2" {
		t.Fatal("independent reserve changed")
	}
}

func TestRetainedSplitReserveProofRejectsUnownedOrGrowingFrontiers(t *testing.T) {
	t.Parallel()
	cases := append(retainedSplitScopeCases(), retainedSplitIdentityCases()...)
	cases = append(cases, retainedSplitNativeCases()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			posting, write, baseline, proof := retainedSplitFixture(t)
			tc.change(&posting, &write, &baseline, &proof)
			before := write.Positions[0].Reserve
			if invalid := ApplyRetainedSplitOpenQuantityReserve(posting, &write, baseline, proof); invalid.Code == "" {
				t.Fatal("unsafe retained split reduction allowed")
			}
			if !SameObservedDecimal(before, write.Positions[0].Reserve) {
				t.Fatal("blocked plan mutated reserve")
			}
		})
	}
}

type retainedSplitMutation struct {
	name   string
	change func(*Posting, *CustomerOrderWrite, *PositionBaseline, *SplitRetainedReservePermission)
}

func retainedSplitScopeCases() []retainedSplitMutation {
	return []retainedSplitMutation{
		{"wrong tenant", func(_ *Posting, _ *CustomerOrderWrite, _ *PositionBaseline, p *SplitRetainedReservePermission) {
			p.Scope.Tenant = "foreign"
		}},
		{"wrong native account", func(_ *Posting, _ *CustomerOrderWrite, b *PositionBaseline, _ *SplitRetainedReservePermission) {
			b.Correlation.Scope.MoyskladAccount = "foreign"
		}},
		{"changed native parent", func(_ *Posting, _ *CustomerOrderWrite, _ *PositionBaseline, p *SplitRetainedReservePermission) {
			p.Correlation.CustomerOrderID = "foreign-order"
		}},
		{"child instead of retained parent", func(p *Posting, _ *CustomerOrderWrite, _ *PositionBaseline, _ *SplitRetainedReservePermission) {
			p.PostingNumber = "actual-child"
		}},
		{"commercial parent changed", func(p *Posting, _ *CustomerOrderWrite, _ *PositionBaseline, _ *SplitRetainedReservePermission) {
			p.CommercialOrderID = "other-order"
		}},
		{"provider ancestor", func(_ *Posting, _ *CustomerOrderWrite, _ *PositionBaseline, p *SplitRetainedReservePermission) {
			p.Before.ParentPostingNumber = Present("ancestor")
		}},
		{"earlier child frontier", func(_ *Posting, _ *CustomerOrderWrite, _ *PositionBaseline, p *SplitRetainedReservePermission) {
			p.Before.ChildPostingNumbers = []string{"earlier-child"}
		}},
		{"current provider ancestor", func(p *Posting, _ *CustomerOrderWrite, _ *PositionBaseline, _ *SplitRetainedReservePermission) {
			p.ParentPostingNumber = Present("ancestor")
		}},
	}
}

func retainedSplitIdentityCases() []retainedSplitMutation {
	return []retainedSplitMutation{
		{"missing split proof", func(_ *Posting, _ *CustomerOrderWrite, _ *PositionBaseline, p *SplitRetainedReservePermission) {
			p.SplitWorkID = ""
		}},
		{"grown retained quantity", func(p *Posting, w *CustomerOrderWrite, _ *PositionBaseline, _ *SplitRetainedReservePermission) {
			p.Lines[0].Quantity = "5"
			w.Positions[0].Quantity = "5"
		}},
		{"current identity changed", func(p *Posting, _ *CustomerOrderWrite, _ *PositionBaseline, _ *SplitRetainedReservePermission) {
			p.Lines[0].Identity.OfferID = "other-offer"
		}},
		{"original identity changed", func(_ *Posting, _ *CustomerOrderWrite, _ *PositionBaseline, p *SplitRetainedReservePermission) {
			p.Before.Lines[0].Identity.OfferID = "other-offer"
		}},
		{"changed mapping revision", func(_ *Posting, w *CustomerOrderWrite, _ *PositionBaseline, _ *SplitRetainedReservePermission) {
			w.Positions[0].Mapping.Version++
		}},
		{"changed native item", func(_ *Posting, w *CustomerOrderWrite, _ *PositionBaseline, _ *SplitRetainedReservePermission) {
			w.Positions[0].Mapping.MoyskladID = "other-item"
		}},
		{"unknown reserve ownership", func(_ *Posting, _ *CustomerOrderWrite, b *PositionBaseline, _ *SplitRetainedReservePermission) {
			b.Bindings[0].Source.Reserve = Field[Decimal]{}
		}},
		{"unknown native reserve", func(_ *Posting, _ *CustomerOrderWrite, b *PositionBaseline, _ *SplitRetainedReservePermission) {
			b.Bindings[0].Native.Reserve = Field[Decimal]{}
		}},
	}
}

func retainedSplitNativeCases() []retainedSplitMutation {
	return []retainedSplitMutation{
		{"unknown native shipped", func(_ *Posting, _ *CustomerOrderWrite, b *PositionBaseline, _ *SplitRetainedReservePermission) {
			b.Bindings[0].Native.Shipped = Field[Decimal]{}
		}},
		{"native shipment", func(_ *Posting, _ *CustomerOrderWrite, b *PositionBaseline, _ *SplitRetainedReservePermission) {
			b.Bindings[0].Native.Shipped = Present(Decimal("1"))
		}},
		{"attested ownership", func(_ *Posting, _ *CustomerOrderWrite, b *PositionBaseline, _ *SplitRetainedReservePermission) {
			b.EvidenceClass = "OPERATOR_ATTESTED"
		}},
		{"reserve increase", func(_ *Posting, _ *CustomerOrderWrite, b *PositionBaseline, _ *SplitRetainedReservePermission) {
			b.Bindings[0].Source.Reserve = Present(Decimal("1"))
			b.Bindings[0].Native.Reserve = Present(Decimal("1"))
		}},
		{"partial cancellation unknown", func(p *Posting, _ *CustomerOrderWrite, _ *PositionBaseline, _ *SplitRetainedReservePermission) {
			p.Lines[0].CancelledQuantity = Present(Decimal("1"))
		}},
		{"changed owned price", func(_ *Posting, w *CustomerOrderWrite, _ *PositionBaseline, _ *SplitRetainedReservePermission) {
			w.Positions[0].Price = Present(Money{Amount: "13", Currency: "RUB"})
		}},
		{"changed owned VAT", func(_ *Posting, w *CustomerOrderWrite, _ *PositionBaseline, _ *SplitRetainedReservePermission) {
			w.Positions[0].VAT = Present(Decimal("20"))
		}},
	}
}

func TestRetainedSplitSealedWriteCannotFillMissingOrChangedReserve(t *testing.T) {
	t.Parallel()
	posting, write, baseline, proof := retainedSplitFixture(t)
	if err := proof.ValidateWrite(posting, write, baseline); !errors.Is(err, ErrStale) {
		t.Fatalf("missing sealed reserve admitted: %v", err)
	}
	if invalid := ApplyRetainedSplitOpenQuantityReserve(posting, &write, baseline, proof); invalid.Code != "" {
		t.Fatal(invalid)
	}
	if err := proof.ValidateWrite(posting, write, baseline); err != nil {
		t.Fatal(err)
	}
	write.Positions[0].Reserve = Present(Decimal("1"))
	if err := proof.ValidateWrite(posting, write, baseline); !errors.Is(err, ErrStale) {
		t.Fatalf("changed sealed reserve admitted: %v", err)
	}
}
