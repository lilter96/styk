package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBusinessPartitionIsExhaustiveDisjointAndKeepsSharedBrowserPortSerial(t *testing.T) {
	t.Parallel()
	listed := []string{"TestAlpha", "ExampleFlow", "FuzzDecoder", "BenchmarkSpeed", "TestOrdersNineProcessStack", "TestOrdersMerchantCurrentStateBrowserStack", "TestHelper"}
	plan, err := partitionBusiness("business", listed, 3)
	if err != nil {
		t.Fatal(err)
	}
	counts, location := make(map[string]int), make(map[string]int)
	for i, names := range plan.Shards {
		pattern := businessPattern(names)
		if strings.Contains(pattern, "/") {
			t.Fatal("partition filtered subtests or fuzz seeds")
		}
		for _, name := range names {
			counts[name]++
			location[name] = i
		}
	}
	for _, name := range plan.Names {
		if counts[name] != 1 {
			t.Fatalf("test omitted or repeated: %s", name)
		}
	}
	if len(plan.Names) != 6 || counts["BenchmarkSpeed"] != 0 || location["TestOrdersNineProcessStack"] != location["TestOrdersMerchantCurrentStateBrowserStack"] {
		t.Fatal("enumerated coverage or fixed-port isolation changed")
	}
	if len(plan.Shards[0]) != 2 {
		t.Fatal("fixed-port browser worker was overloaded with unrelated tests")
	}
	if _, err = partitionBusiness("business", []string{"TestA", "TestA"}, 2); err == nil {
		t.Fatal("duplicate compiled test was accepted")
	}
}

func TestBusinessPartitionSeparatesMeasuredHeavyCases(t *testing.T) {
	t.Parallel()
	heavy := []string{"TestOrdersExemplarHTTPSealedPreparationAndSetReconcile", "TestOrdersNativeNineProcessStack", "TestPasswordResetInvalidatesSessionsAndOldAuthentications"}
	listed := append([]string{"TestOrdersNineProcessStack", "TestOrdersMerchantCurrentStateBrowserStack", "TestTinyA", "TestTinyB"}, heavy...)
	plan, err := partitionBusiness("business", listed, 4)
	if err != nil {
		t.Fatal(err)
	}
	workers := make(map[int]bool, len(heavy))
	for i, names := range plan.Shards {
		for _, name := range names {
			for _, target := range heavy {
				if name == target {
					workers[i] = true
				}
			}
		}
	}
	if len(workers) != len(heavy) || workers[0] {
		t.Fatal("measured heavy cases compete in the same worker or the reserved browser worker")
	}
}

func TestBusinessCoverageRejectsOmissionDuplicationAndForeignTests(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, data string
		valid      bool
	}{
		{"complete seeds and helper skip", "{\"Test\":\"FuzzA/seed#0\",\"Action\":\"pass\"}\n{\"Test\":\"FuzzA\",\"Action\":\"pass\"}\n{\"Test\":\"TestHelper\",\"Action\":\"skip\"}\n", true},
		{"missing", "{\"Test\":\"FuzzA\",\"Action\":\"pass\"}\n", false},
		{"duplicate", "{\"Test\":\"FuzzA\",\"Action\":\"pass\"}\n{\"Test\":\"FuzzA\",\"Action\":\"pass\"}\n{\"Test\":\"TestHelper\",\"Action\":\"skip\"}\n", false},
		{"foreign", "{\"Test\":\"FuzzA\",\"Action\":\"pass\"}\n{\"Test\":\"TestHelper\",\"Action\":\"skip\"}\n{\"Test\":\"TestOther\",\"Action\":\"pass\"}\n", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "worker.jsonl")
			if err := os.WriteFile(path, []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := verifyBusinessCoverage(path, []string{"FuzzA", "TestHelper"}); (err == nil) != tc.valid {
				t.Fatalf("terminal coverage verdict: %v", err)
			}
		})
	}
}

func TestFullPackagePartitionRetainsEveryOrdinaryPackageOnce(t *testing.T) {
	t.Parallel()
	other, err := partitionPackages([]string{"a", "business", "c"}, "business")
	if err != nil || !reflect.DeepEqual(other, []string{"a", "c"}) {
		t.Fatalf("package partition: %v %v", other, err)
	}
	for _, all := range [][]string{{"a", "a", "business"}, {"a", "c"}, {"business"}} {
		if _, err := partitionPackages(all, "business"); err == nil {
			t.Fatalf("incomplete or duplicate full package set accepted: %v", all)
		}
	}
}
