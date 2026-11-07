package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type financeWorkerProbe struct {
	name    string
	calls   *[]string
	failure error
}

func (p financeWorkerProbe) RunNext(context.Context) (bool, error) {
	*p.calls = append(*p.calls, p.name)
	return true, p.failure
}

func (p financeWorkerProbe) Tick(context.Context) error {
	*p.calls = append(*p.calls, p.name+":tick")
	return p.failure
}

func TestFinanceWorkerRotatesBusyDurableSubsystems(t *testing.T) {
	t.Parallel()
	calls := make([]string, 0, 4)
	orderError := errors.New("synthetic order failure")
	work := ordersWork{runner: financeWorkerProbe{name: "orders", calls: &calls, failure: orderError}, finance: financeWorkerProbe{name: "finance", calls: &calls}}
	for index := range 4 {
		worked, err := work.dispatchNext(context.Background())
		if !worked || !errors.Is(err, orderError) && index%2 == 0 || err != nil && index%2 != 0 {
			t.Fatal("unexpected independent runner result", index, worked, err)
		}
	}
	if !reflect.DeepEqual(calls, []string{"orders", "finance", "orders", "finance"}) {
		t.Fatal("busy order outbox starved finance", calls)
	}
}

func TestFinanceWorkerSchedulesBothSubsystemsAfterFailure(t *testing.T) {
	t.Parallel()
	calls := make([]string, 0, 2)
	failure := errors.New("synthetic order schedule failure")
	work := ordersWork{runner: financeWorkerProbe{name: "orders", calls: &calls, failure: failure}, finance: financeWorkerProbe{name: "finance", calls: &calls}}
	if err := work.tick(context.Background()); !errors.Is(err, failure) {
		t.Fatal("scheduler failure lost", err)
	}
	if !reflect.DeepEqual(calls, []string{"orders:tick", "finance:tick"}) {
		t.Fatal("mirror scheduling failure starved finance", calls)
	}
}

func TestCarriageWorkerRotatesWithBusyOrderAndFinance(t *testing.T) {
	t.Parallel()
	calls := make([]string, 0, 6)
	work := ordersWork{runner: financeWorkerProbe{name: "orders", calls: &calls}, finance: financeWorkerProbe{name: "finance", calls: &calls}, carriage: financeWorkerProbe{name: "carriage", calls: &calls}}
	for range 6 {
		worked, err := work.dispatchNext(context.Background())
		if err != nil || !worked {
			t.Fatal(worked, err)
		}
	}
	if !reflect.DeepEqual(calls, []string{"orders", "finance", "carriage", "orders", "finance", "carriage"}) {
		t.Fatal("durable subsystem starved", calls)
	}
}

func TestCarriageWorkerDispatchesWithoutOtherSubsystems(t *testing.T) {
	t.Parallel()
	calls := make([]string, 0, 1)
	work := ordersWork{carriage: financeWorkerProbe{name: "carriage", calls: &calls}}
	worked, err := work.dispatchNext(context.Background())
	if err != nil || !worked || !reflect.DeepEqual(calls, []string{"carriage"}) {
		t.Fatal(worked, err, calls)
	}
	empty := ordersWork{}
	if worked, err = empty.dispatchNext(context.Background()); err != nil || worked {
		t.Fatal(worked, err)
	}
}
