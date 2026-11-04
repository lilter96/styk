package postgres

import (
	"strings"
	"testing"
)

func TestUnassembledSplitJoinsAncestorFencesWithoutUnmirroredReserveAuthority(t *testing.T) {
	t.Parallel()
	both := "IN ('posting_split','posting_split_unassembled')"
	for _, test := range []struct{ name, query string }{{"dependency", splitReserveBlocksSQL}, {"retained", retainedSplitReserveSQL}, {"superseded", supersededSplitReserveSQL}} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if !strings.Contains(test.query, "ancestor.action "+both) {
				t.Fatal("new split action is absent from ancestor fences")
			}
			if !strings.Contains(test.query, "w.action "+both) && !strings.Contains(test.query, "split.action "+both) {
				t.Fatal("latest split selection ignores new pending work")
			}
		})
	}
	if !strings.Contains(splitReserveBlocksSQL, "(split.action='posting_split' AND split.state='RECONCILED'") {
		t.Fatal("new unassembled action acquired legacy unencumbered reserve authority")
	}
}
