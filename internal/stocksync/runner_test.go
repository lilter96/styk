package stocksync

import (
	"context"
	"testing"
)

func TestRunnerDisabledByDefaultBeforeDatabaseOrNetwork(t *testing.T) {
	worked, err := (Runner{}).RunNext(context.Background())
	if worked || err != nil {
		t.Fatalf("disabled runner did work or accessed a dependency: worked=%v err=%v", worked, err)
	}
}
