package testpublication

import "testing"

func TestFixedTestAccountAndTargetCannotBeWidened(t *testing.T) {
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "")
	if Enabled(Scope()) {
		t.Fatal("test publication was not default-off")
	}
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "true")
	if !TargetAllowed(Scope(), OzonProduct, OzonWarehouse, SourceProduct, SourceWarehouse, "7") {
		t.Fatal("approved test target rejected")
	}
	for _, quantity := range []string{"-1", "8", "01", "", "1.0"} {
		if TargetAllowed(Scope(), OzonProduct, OzonWarehouse, SourceProduct, SourceWarehouse, quantity) {
			t.Fatalf("unbounded quantity %q accepted", quantity)
		}
	}
	for _, change := range []func(*string){
		func(x *string) { *x = "wrong" },
	} {
		for i := 0; i < 7; i++ {
			scope := Scope()
			product, warehouse, source, sourceWarehouse := OzonProduct, OzonWarehouse, SourceProduct, SourceWarehouse
			fields := []*string{&scope.Tenant, &scope.MoyskladAccount, &scope.OzonAccount, &product, &warehouse, &source, &sourceWarehouse}
			change(fields[i])
			if TargetAllowed(scope, product, warehouse, source, sourceWarehouse, "1") {
				t.Fatalf("wrong scope/target field %d accepted", i)
			}
		}
	}
}
