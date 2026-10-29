package catalogsync

import (
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

func TestProviderVerifierCanKeepReadbackUnverified(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		unavailable bool
	}{
		{name: "provider fact not observed"},
		{name: "provider verification unavailable", unavailable: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.approve(t)
			f.run(t)
			if tc.unavailable {
				f.target.verificationErr = errTestProvider
			} else {
				f.target.verification = []catalog.FieldError{{Field: catalog.FieldName, Code: "FIELD_READBACK_UNAVAILABLE"}}
			}
			f.run(t)
			if f.target.verifications != 1 || f.store.works[0].State != StateAcknowledged || f.store.current.Targets[0].Baseline != nil || f.target.writes != 1 {
				t.Fatal("provider verification failure was ignored or triggered another write")
			}
		})
	}
}
