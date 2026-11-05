package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRequiredCIGateRejectsFailedCancelledAndSelectedSkipped(t *testing.T) {
	t.Parallel()
	script := workflowGate(t)
	tests := []struct {
		name, result string
		required     bool
		pass         bool
	}{
		{"unrelated skipped", "skipped", false, true},
		{"selected succeeded", "success", true, true},
		{"selected failed", "failure", true, false},
		{"selected cancelled", "cancelled", true, false},
		{"selected skipped", "skipped", true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			jobs := gateFixture(tc.result, tc.required)
			data, err := json.Marshal(jobs)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "python3", "-c", script)
			cmd.Env = append(cleanEnvironment(), "RESULTS="+string(data))
			if err = cmd.Run(); (err == nil) != tc.pass {
				t.Fatalf("required gate outcome: %v", err)
			}
		})
	}
}

func gateFixture(result string, required bool) map[string]any {
	value := "false"
	if required {
		value = "true"
	}
	jobs := map[string]any{"plan": map[string]any{"result": "success", "outputs": map[string]string{"go": value}}}
	for _, name := range []string{"go", "web", "integration", "full"} {
		jobs[name] = map[string]string{"result": "skipped"}
	}
	jobs["go"] = map[string]string{"result": result}
	return jobs
}

func workflowGate(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("CI gate uses the Python standard library on ubuntu-latest")
	}
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	_, script, ok := strings.Cut(string(data), "python3 - <<'PY'\n")
	if !ok {
		t.Fatal("required CI gate is missing")
	}
	script, _, ok = strings.Cut(script, "          PY")
	if !ok {
		t.Fatal("required CI gate terminator is missing")
	}
	return strings.ReplaceAll(script, "          ", "")
}
