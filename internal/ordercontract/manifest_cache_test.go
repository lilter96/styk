package ordercontract

import "testing"

func BenchmarkContractManifest(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := ContractManifest(); err != nil {
			b.Fatal(err)
		}
	}
}

func TestContractManifestReturnsIndependentNestedCopies(t *testing.T) {
	t.Parallel()
	first, err := ContractManifest()
	if err != nil {
		t.Fatal(err)
	}
	wanted := first.Contracts[0].Fields[0].Types[0]
	first.Sources[0].Evidence = "edited"
	first.Contracts[0].Sources[0] = "edited"
	first.Contracts[0].Fields[0].Types[0] = "edited"
	first.Contracts[0].Fields[0].Sources[0] = "edited"
	first.Capabilities[0].Prerequisites[0] = "edited"
	first.Capabilities[0].Sources[0] = "edited"
	first.Limitations[0] = "edited"
	second, err := ContractManifest()
	if err != nil {
		t.Fatal(err)
	}
	if second.Sources[0].Evidence == "edited" || second.Contracts[0].Sources[0] == "edited" || second.Contracts[0].Fields[0].Types[0] != wanted || second.Contracts[0].Fields[0].Sources[0] == "edited" || second.Capabilities[0].Prerequisites[0] == "edited" || second.Capabilities[0].Sources[0] == "edited" || second.Limitations[0] == "edited" {
		t.Fatal("embedded cache leaked mutable nested state")
	}
}
