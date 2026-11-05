package orders

import (
	"slices"
	"testing"
)

func TestSplitTransferRetainsOriginalProductIdentityAcrossAllOutputs(t *testing.T) {
	t.Parallel()
	for _, substitute := range []bool{false, true} {
		t.Run(map[bool]string{false: "matching product", true: "consistent substitution"}[substitute], func(t *testing.T) {
			t.Parallel()
			facts := splitTransferFixture(t)
			facts.Before.Lines = slices.Clone(facts.Before.Lines)
			facts.Before.Lines[0].ProductID = Present("original-product")
			actual := "original-product"
			if substitute {
				actual = "substitute-product"
			}
			for i := range facts.Split.Receipt.ObservedPostings {
				p := &facts.Split.Receipt.ObservedPostings[i]
				p.Lines = slices.Clone(p.Lines)
				p.Lines[0].ProductID = Present(actual)
			}
			facts.Parent.Lines = slices.Clone(facts.Parent.Lines)
			facts.Child.Lines = slices.Clone(facts.Child.Lines)
			facts.Parent.Lines[0].ProductID = Present(actual)
			facts.Child.Lines[0].ProductID = Present(actual)
			_, err := NewSplitReserveTransfer(facts)
			if (err != nil) != substitute {
				t.Fatalf("substitute=%v err=%v", substitute, err)
			}
		})
	}
}
