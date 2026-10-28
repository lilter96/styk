package domain

import (
	"testing"
	"time"
)

func TestValidation(t *testing.T) {
	o := Observation{OperationKey: "key", Source: "demo", SKU: "sku", Quantity: 1}
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	o.Quantity = -1
	if o.Validate() == nil {
		t.Fatal("negative quantity accepted")
	}
	o.Quantity = 1
	o.SKU = ""
	if o.Validate() == nil {
		t.Fatal("empty sku accepted")
	}
}
func TestRetry(t *testing.T) {
	if FailureState(1, 3) != Pending || FailureState(3, 3) != Failed {
		t.Fatal("incorrect failure transition")
	}
	now := time.Unix(0, 0)
	if !RetryAt(now, time.Second).Equal(now.Add(time.Second)) {
		t.Fatal("incorrect retry delay")
	}
}
