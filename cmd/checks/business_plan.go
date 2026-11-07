package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

const businessWorkers = 8

type businessPlan struct {
	Package               string     `json:"package"`
	Names                 []string   `json:"names"`
	Shards                [][]string `json:"shards"`
	EstimatedMilliseconds []int64    `json:"estimated_milliseconds"`
}

func fullPackagePartition(ctx context.Context, root string) ([]string, string, error) {
	data, err := output(ctx, root, "go", "list", "-tags=integration", "./...")
	if err != nil {
		return nil, "", err
	}
	business, err := output(ctx, root, "go", "list", "-tags=integration", "./tests/integration")
	if err != nil {
		return nil, "", err
	}
	name := strings.TrimSpace(string(business))
	other, err := partitionPackages(strings.Fields(string(data)), name)
	return other, name, err
}

func partitionPackages(all []string, business string) ([]string, error) {
	other, seen, found := make([]string, 0, len(all)), make(map[string]bool, len(all)), false
	for _, name := range all {
		if name == "" || seen[name] {
			return nil, errors.New("full package enumeration contains an empty or duplicate package")
		}
		seen[name] = true
		if name == business {
			found = true
			continue
		}
		other = append(other, name)
	}
	if !found || len(other) == 0 {
		return nil, errors.New("full package partition omitted the business or ordinary packages")
	}
	return other, nil
}

func enumerateBusiness(ctx context.Context, root, binary, pkg string) (businessPlan, error) {
	data, err := output(ctx, root, binary, "-test.list=.")
	if err != nil {
		return businessPlan{}, err
	}
	return partitionBusiness(pkg, strings.Fields(string(data)), businessWorkers)
}

func partitionBusiness(pkg string, listed []string, workers int) (businessPlan, error) {
	if workers < 1 {
		return businessPlan{}, errors.New("business worker count must be positive")
	}
	plan := businessPlan{Package: pkg, Names: make([]string, 0, len(listed)), Shards: make([][]string, workers), EstimatedMilliseconds: make([]int64, workers)}
	seen := make(map[string]bool, len(listed))
	for _, name := range listed {
		if !strings.HasPrefix(name, "Test") && !strings.HasPrefix(name, "Example") && !strings.HasPrefix(name, "Fuzz") {
			continue // Benchmarks are not executed by the ordinary full suite.
		}
		if seen[name] {
			return plan, fmt.Errorf("duplicate business test in compiled enumeration: %s", name)
		}
		seen[name] = true
		plan.Names = append(plan.Names, name)
	}
	if len(plan.Names) == 0 {
		return plan, errors.New("compiled business binary enumerated no tests")
	}
	plan.assignBalanced()
	return plan, nil
}

func fixedPortBusiness(name string) bool {
	return name == "TestOrdersNineProcessStack" || name == "TestOrdersMerchantCurrentStateBrowserStack"
}

func (plan *businessPlan) assignBalanced() {
	names := append([]string(nil), plan.Names...)
	sort.Slice(names, func(i, j int) bool {
		if businessCost(names[i]) == businessCost(names[j]) {
			return names[i] < names[j]
		}
		return businessCost(names[i]) > businessCost(names[j])
	})
	reserved := false
	for _, name := range names {
		reserved = reserved || fixedPortBusiness(name)
	}
	for _, name := range names {
		worker := plan.leastLoadedWorker(reserved && !fixedPortBusiness(name))
		if fixedPortBusiness(name) {
			worker = 0
		}
		plan.Shards[worker] = append(plan.Shards[worker], name)
		plan.EstimatedMilliseconds[worker] += businessCost(name)
	}
}

func (plan businessPlan) leastLoadedWorker(reserveBrowser bool) int {
	first := 0
	if reserveBrowser && len(plan.Shards) > 1 {
		first = 1
	}
	worker := first
	for i := first + 1; i < len(plan.Shards); i++ {
		if plan.EstimatedMilliseconds[i] < plan.EstimatedMilliseconds[worker] {
			worker = i
		}
	}
	return worker
}

func businessPattern(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, regexp.QuoteMeta(name))
	}
	return "^(" + strings.Join(quoted, "|") + ")$"
}

func verifyBusinessCoverage(path string, expected []string) (err error) {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open business coverage: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	counts := make(map[string]int, len(expected))
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var event struct{ Test, Action string }
		if err = json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return fmt.Errorf("decode business coverage: %w", err)
		}
		if event.Test != "" && !strings.Contains(event.Test, "/") && (event.Action == "pass" || event.Action == "skip" || event.Action == "fail") {
			counts[event.Test]++
		}
	}
	if err = scanner.Err(); err != nil {
		return fmt.Errorf("read business coverage: %w", err)
	}
	return exactBusinessCoverage(counts, expected)
}

func exactBusinessCoverage(counts map[string]int, expected []string) error {
	for _, name := range expected {
		if counts[name] != 1 {
			return fmt.Errorf("business test %s has %d terminal outcomes; expected exactly one", name, counts[name])
		}
	}
	if len(counts) != len(expected) {
		return errors.New("business worker executed tests outside its exhaustive disjoint partition")
	}
	return nil
}
