package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/lilter96/styk/internal/checkpolicy"
)

func repositoryRoot(ctx context.Context) (string, error) {
	data, err := output(ctx, "", "git", "rev-parse", "--show-toplevel")
	return strings.TrimSpace(string(data)), err
}

func buildPlan(ctx context.Context, root string, o options) (checkpolicy.Plan, error) {
	files, err := changedFiles(ctx, root, o)
	if err != nil {
		return checkpolicy.Plan{}, err
	}
	p := checkpolicy.Select(files)
	if err = p.ResolveIntegration(ctx, root); err != nil {
		return p, err
	}
	if !p.NeedsGo() {
		return p, nil
	}
	packages, module, err := packageGraph(ctx, root)
	if err != nil {
		return p, err
	}
	err = p.ResolveGo(root, module, packages)
	return p, err
}

func changedFiles(ctx context.Context, root string, o options) ([]string, error) {
	args := []string{"diff", "--no-renames", "--name-only", "-z"}
	base := o.base
	if base == "" {
		base = "HEAD"
	}
	if o.head != "" {
		resolved, err := comparisonBase(ctx, root, base, o.head)
		if err != nil {
			return nil, err
		}
		args = append(args, resolved, o.head)
	} else {
		resolved, err := output(ctx, root, "git", "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
		if err != nil {
			return nil, err
		}
		args = append(args, strings.TrimSpace(string(resolved)))
	}
	data, err := output(ctx, root, "git", append(args, "--")...)
	if err != nil {
		return nil, err
	}
	if o.head == "" {
		untracked, err := output(ctx, root, "git", "ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return nil, err
		}
		data = append(data, untracked...)
	}
	return strings.FieldsFunc(string(data), func(r rune) bool { return r == 0 }), nil
}

func comparisonBase(ctx context.Context, root, base, head string) (string, error) {
	for _, ref := range []string{head, base} {
		if ref == strings.Repeat("0", 40) {
			continue
		}
		if _, err := output(ctx, root, "git", "rev-parse", "--verify", "--end-of-options", ref+"^{commit}"); err != nil {
			return "", err
		}
	}
	if base == strings.Repeat("0", 40) {
		return "4b825dc642cb6eb9a060e54bf8d69288fbee4904", nil
	}
	data, err := output(ctx, root, "git", "merge-base", base, head)
	return strings.TrimSpace(string(data)), err
}

func packageGraph(ctx context.Context, root string) ([]checkpolicy.Package, string, error) {
	data, err := output(ctx, root, "go", "list", "-json", "./...")
	if err != nil {
		return nil, "", err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	packages := make([]checkpolicy.Package, 0, 64)
	for {
		var pkg checkpolicy.Package
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, "", fmt.Errorf("decode Go package graph: %w", err)
		}
		packages = append(packages, pkg)
	}
	module, err := output(ctx, root, "go", "list", "-m")
	name := strings.TrimSpace(string(module))
	if err == nil && (name == "" || strings.ContainsAny(name, "\r\n")) {
		return nil, "", fmt.Errorf("cannot select a unique Go module; use GOWORK=off for this repository")
	}
	return packages, name, err
}

func output(ctx context.Context, root, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	configureProcess(cmd)
	cmd.Dir, cmd.Env = root, cleanEnvironment()
	data, err := cmd.Output()
	err = errors.Join(err, finishProcess(cmd))
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w", name, err)
	}
	return data, nil
}
