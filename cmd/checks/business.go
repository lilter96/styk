package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func fullBusinessChecks(ctx context.Context, root string, env, other []string, pkg, binary string) (err error) {
	plan, err := enumerateBusiness(ctx, root, binary, pkg)
	if err != nil {
		return err
	}
	artifacts, err := businessArtifacts(root, plan)
	if err != nil {
		return err
	}
	databases := businessDatabases()
	defer func() { err = errors.Join(err, removeBusinessDatabases(env, databases)) }()
	checks := make([]namedCheck, 0, len(plan.Shards)+1)
	checks = append(checks, namedCheck{"Other Go packages", func(ctx context.Context) error {
		args := fullUnitCommand(other)
		return command(ctx, root, env, args[0], args[1:]...)
	}})
	for i, names := range plan.Shards {
		if len(names) == 0 {
			continue
		}
		checks = append(checks, namedCheck{fmt.Sprintf("Business worker %d", i+1), func(ctx context.Context) error {
			isolated, err := prepareBusinessDatabase(ctx, root, env, databases[i])
			if err != nil {
				return err
			}
			return runBusinessWorker(ctx, root, isolated, pkg, binary, filepath.Join(artifacts, fmt.Sprintf("worker-%d.jsonl", i+1)), names)
		}})
	}
	return concurrentChecks(ctx, checks)
}

func businessArtifacts(root string, plan businessPlan) (string, error) {
	parent := filepath.Join(root, ".local", "checks")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("create verification artifact directory: %w", err)
	}
	dir, err := os.MkdirTemp(parent, "full-")
	if err != nil {
		return "", fmt.Errorf("create full coverage directory: %w", err)
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode full coverage manifest: %w", err)
	}
	if err = os.WriteFile(filepath.Join(dir, "coverage.json"), data, 0o600); err != nil {
		return "", fmt.Errorf("save full coverage manifest: %w", err)
	}
	fmt.Printf("Business coverage: %d compiled Test/Example/Fuzz names, %d disjoint workers; artifacts %s\n", len(plan.Names), len(plan.Shards), dir)
	fmt.Printf("Estimated business worker loads (ms): %v\n", plan.EstimatedMilliseconds)
	return dir, nil
}

func runBusinessWorker(ctx context.Context, root string, env []string, pkg, binary, path string, names []string) (err error) {
	timeout, err := businessTimeout(ctx)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create business worker output: %w", err)
	}
	started := time.Now()
	args := []string{"tool", "test2json", "-t", "-p", pkg, binary, "-test.v=test2json", "-test.count=1", "-test.timeout=" + timeout, "-test.parallel=2", "-test.run=" + businessPattern(names)}
	cmd := exec.CommandContext(ctx, "go", args...)
	configureProcess(cmd)
	cmd.Dir, cmd.Env = filepath.Join(root, "tests", "integration"), append(cleanEnvironment(), env...)
	cmd.Stdout, cmd.Stderr = file, file
	callErr := cmd.Run()
	groupErr := finishProcess(cmd)
	closeErr := file.Close()
	coverageErr := verifyBusinessCoverage(path, names)
	if err = errors.Join(callErr, groupErr, closeErr, coverageErr); err != nil {
		return fmt.Errorf("business worker failed after %s; log %s: %w", time.Since(started).Round(time.Millisecond), path, err)
	}
	fmt.Printf("Business coverage verified: %d/%d top-level terminal outcomes in %s; log %s\n", len(names), len(names), time.Since(started).Round(time.Millisecond), path)
	return nil
}

func businessTimeout(ctx context.Context) (string, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return "", errors.New("business workers require the shared verification deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return "", context.DeadlineExceeded
	}
	return remaining.String(), nil
}

func fullUnitCommand(packages []string) []string {
	return append([]string{"go", "test", "-json", "-race", "-tags=integration", "-count=1", "-timeout=4m", "-p=4"}, packages...)
}
