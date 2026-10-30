package ozon

import (
	"testing"
)

func TestDecodePostingCancellationReasons(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		payload string
		want    int
		valid   bool
	}{
		{"valid exact posting", `{"result":[{"posting_number":"p-1","reasons":[{"id":352,"title":"Нет товара","type_id":"seller"}]}]}`, 1, true},
		{"empty reasons", `{"result":[{"posting_number":"p-1","reasons":[]}]}`, 0, true},
		{"wrong posting", `{"result":[{"posting_number":"p-2","reasons":[]}]}`, 0, false},
		{"missing reasons", `{"result":[{"posting_number":"p-1"}]}`, 0, false},
		{"duplicate id", `{"result":[{"posting_number":"p-1","reasons":[{"id":352,"title":"A"},{"id":352,"title":"B"}]}]}`, 0, false},
		{"invalid id", `{"result":[{"posting_number":"p-1","reasons":[{"id":0,"title":"A"}]}]}`, 0, false},
		{"multiple postings", `{"result":[{"posting_number":"p-1","reasons":[]},{"posting_number":"p-2","reasons":[]}]}`, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := decodeActionReasons([]byte(tc.payload), "p-1")
			if (err == nil) != tc.valid || len(got) != tc.want {
				t.Fatalf("reasons=%v error=%v", got, err)
			}
		})
	}
}
