package orderactions

import (
	"errors"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestFBSInstancesRequireExplicitPerUnitCompleteness(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func([]FBSInstanceRequirement, []FBSLineInstances)
		want   error
	}{
		{name: "complete", change: func([]FBSInstanceRequirement, []FBSLineInstances) {}},
		{name: "unknown marking requirement", change: func(r []FBSInstanceRequirement, _ []FBSLineInstances) { r[0].MarkingRequired = orders.Field[bool]{} }, want: orders.ErrBlocked},
		{name: "missing mark", change: func(_ []FBSInstanceRequirement, i []FBSLineInstances) {
			i[0].Instances[0].MarkingCode = orders.Field[string]{}
		}, want: orders.ErrBlocked},
		{name: "missing country", change: func(_ []FBSInstanceRequirement, i []FBSLineInstances) {
			i[0].Instances[0].Country = orders.Field[string]{}
		}, want: orders.ErrBlocked},
		{name: "missing customs", change: func(_ []FBSInstanceRequirement, i []FBSLineInstances) {
			i[0].Instances[0].Declaration = orders.Field[string]{}
		}, want: orders.ErrBlocked},
		{name: "repeated instance", change: func(_ []FBSInstanceRequirement, i []FBSLineInstances) { i[0].Instances[1].ID = i[0].Instances[0].ID }, want: orders.ErrConflict},
		{name: "repeated marking", change: func(_ []FBSInstanceRequirement, i []FBSLineInstances) {
			i[0].Instances[1].MarkingCode = i[0].Instances[0].MarkingCode
		}, want: orders.ErrConflict},
		{name: "missing unit", change: func(_ []FBSInstanceRequirement, i []FBSLineInstances) { i[0].Instances = i[0].Instances[:2] }, want: orders.ErrConflict},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			posting, requirements, inputs := fbsInstanceFixture(t)
			tc.change(requirements, inputs)
			if err := ValidateFBSInstances(posting, requirements, inputs); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestFBSInstancesDoNotAssumeMissingRequirementsAreFalse(t *testing.T) {
	t.Parallel()
	posting, requirements, _ := fbsInstanceFixture(t)
	if err := ValidateFBSInstances(posting, nil, nil); !errors.Is(err, orders.ErrBlocked) {
		t.Fatalf("absent source requirements accepted: %v", err)
	}
	if err := ValidateFBSInstances(posting, requirements, nil); !errors.Is(err, orders.ErrBlocked) {
		t.Fatalf("required instances absent: %v", err)
	}
	for index := range requirements {
		requirements[index].InstancesRequired = orders.Present(false)
		requirements[index].MarkingRequired = orders.Present(false)
		requirements[index].CountryRequired = orders.Present(false)
		requirements[index].DeclarationRequired = orders.Present(false)
	}
	if err := ValidateFBSInstances(posting, requirements, nil); err != nil {
		t.Fatalf("explicit false requirements rejected: %v", err)
	}
}

func fbsInstanceFixture(t *testing.T) (orders.Posting, []FBSInstanceRequirement, []FBSLineInstances) {
	t.Helper()
	posting, _, _ := fbsFixture(t)
	identity := posting.Lines[0].Identity
	requirements := []FBSInstanceRequirement{{Identity: identity, InstancesRequired: orders.Present(true), MarkingRequired: orders.Present(true),
		CountryRequired: orders.Present(true), DeclarationRequired: orders.Present(true)}}
	instances := make([]FBSInstance, 0, 3)
	for _, id := range []string{"900719925474099312349", "900719925474099312350", "900719925474099312351"} {
		instances = append(instances, FBSInstance{ID: id, MarkingCode: orders.Present("fixture-mark\x1d" + id), Country: orders.Present("fixture-country"), Declaration: orders.Present("fixture-customs")})
	}
	return posting, requirements, []FBSLineInstances{{Identity: identity, Instances: instances}}
}
