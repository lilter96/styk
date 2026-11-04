package orders

import "testing"

func TestNativePostedProductRequiresActualTrackingFacts(t *testing.T) {
	t.Parallel()
	command, _ := nativeDomainFixture(t)
	proof := command.NativeMovement.Products[0]
	for _, test := range []struct {
		name   string
		change func(*NativeProductRequirements)
	}{
		{"unknown serial", func(p *NativeProductRequirements) { p.SerialTrackable = Field[bool]{} }},
		{"serial", func(p *NativeProductRequirements) { p.SerialTrackable = Present(true) }},
		{"marked", func(p *NativeProductRequirements) { p.TrackingType = Present("TOBACCO") }},
		{"unknown enum", func(p *NativeProductRequirements) { p.TrackingType = Present("FUTURE_ENUM") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := proof
			test.change(&p)
			if p.Allows(ActionDemandCreate, true) {
				t.Fatal("unsupported physical requirement accepted")
			}
			if !p.Allows(ActionDemandCreate, false) {
				t.Fatal("physical-only uncertainty blocked harmless draft")
			}
		})
	}
}
