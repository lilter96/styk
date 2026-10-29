package postgres

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestValidateOrderSelection(t *testing.T) {
	t.Parallel()
	tooMany := make([]string, orders.MaxPagePostings+1)
	for index := range tooMany {
		tooMany[index] = fmt.Sprint(index)
	}
	tests := []struct {
		name    string
		numbers []string
		valid   bool
	}{{name: "default batch", valid: true}, {name: "exact identifiers", numbers: []string{"posting-101", "posting-001"}, valid: true},
		{name: "duplicate", numbers: []string{"posting", "posting"}}, {name: "empty", numbers: []string{""}},
		{name: "whitespace", numbers: []string{" posting "}}, {name: "control", numbers: []string{"posting\nother"}}, {name: "unbounded", numbers: tooMany}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validateOrderSelection(test.numbers)
			if (err == nil) != test.valid || err != nil && !errors.Is(err, orders.ErrInvalid) {
				t.Fatalf("selection validity=%v, want %v: %v", err == nil, test.valid, err)
			}
		})
	}
}
