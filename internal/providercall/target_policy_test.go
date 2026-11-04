package providercall

import (
	"testing"
	"time"
)

func TestStockTargetCooldownUsesExactValidatedMethodPolicy(t *testing.T) {
	t.Parallel()
	request := fixtureRequest()
	request.Scope.Provider = "ozon"
	request.Method, request.Operation, request.TargetKey, request.IsWrite = "POST", "v2/products/stocks", "product:warehouse", true
	attempt, err := normalize(request, Options{MaxInflight: 1})
	if err != nil {
		t.Fatal(err)
	}
	attempt.TargetInterval = time.Nanosecond
	if StockTargetCooldown(attempt) != 120*time.Second {
		t.Fatal("untrusted requested interval weakened policy")
	}
	mutations := []func(*Attempt){
		func(a *Attempt) { a.Scope.Provider = "moysklad" }, func(a *Attempt) { a.IsWrite = false },
		func(a *Attempt) { a.TargetKey = "" }, func(a *Attempt) { a.Method = digest("GET\x00v2/products/stocks") },
		func(a *Attempt) { a.Operation = digest("v1/posting/fbs/split") },
	}
	for _, mutate := range mutations {
		changed := attempt
		mutate(&changed)
		if StockTargetCooldown(changed) != 0 {
			t.Fatal("unrelated operation received stock policy")
		}
	}
}
