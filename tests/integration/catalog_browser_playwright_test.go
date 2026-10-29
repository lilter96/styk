//go:build integration

package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func runCatalogBrowserPlaywright(t *testing.T, ctx context.Context, apiURL string) {
	t.Helper()
	web, err := filepath.Abs("../../web")
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.CreateTemp(filepath.Join(web, "node_modules/.cache"), "catalog-stack-vite-*.mjs")
	if err != nil {
		t.Fatal(err)
	}
	defer removeCatalogBrowserConfig(t, config.Name())
	if _, err = fmt.Fprintf(config, `import {defineConfig} from 'vite'; export default defineConfig({server:{host:'127.0.0.1',port:15180,strictPort:true,proxy:{'/api':{target:%q,changeOrigin:false}}}});`, apiURL); err != nil {
		t.Fatal(err)
	}
	if err = config.Close(); err != nil {
		t.Fatal(err)
	}
	vite := exec.CommandContext(ctx, "node", "node_modules/vite/bin/vite.js", "--config", config.Name())
	vite.Dir = web
	var logs catalogBrowserLogs
	vite.Stdout, vite.Stderr = &logs, &logs
	if err = vite.Start(); err != nil {
		t.Fatal(err)
	}
	defer stopCatalogBrowserVite(t, vite)
	waitCatalogBrowserVite(t, ctx, &logs)
	runCatalogBrowserSpec(t, ctx, web, apiURL)
}

func runCatalogBrowserSpec(t *testing.T, ctx context.Context, web, apiURL string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "e2e/catalog-stack.spec.ts", "--workers=1", "--reporter=line")
	cmd.Dir = web
	cmd.Env = append(os.Environ(), "STYK_CATALOG_STACK_ORIGIN="+catalogBrowserOrigin, "STYK_CATALOG_STACK_API="+apiURL,
		"STYK_CATALOG_STACK_SECRET=synthetic-browser-harness")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("catalog merchant browser stack failed: %v\n%s", err, output)
	}
	t.Log(string(output))
}

func waitCatalogBrowserVite(t *testing.T, ctx context.Context, logs *catalogBrowserLogs) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	for range 100 {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogBrowserOrigin, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err == nil {
			if err = response.Body.Close(); err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("catalog browser Vite unavailable: %s", logs.String())
}

type catalogBrowserLogs struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (logs *catalogBrowserLogs) Write(data []byte) (int, error) {
	logs.mu.Lock()
	defer logs.mu.Unlock()
	return logs.buffer.Write(data)
}

func (logs *catalogBrowserLogs) String() string {
	logs.mu.Lock()
	defer logs.mu.Unlock()
	return logs.buffer.String()
}

func stopCatalogBrowserVite(t *testing.T, vite *exec.Cmd) {
	t.Helper()
	if err := vite.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("stop catalog Vite: %v", err)
	}
	var exit *exec.ExitError
	if err := vite.Wait(); err != nil && !errors.As(err, &exit) {
		t.Errorf("wait for catalog Vite: %v", err)
	}
}

func removeCatalogBrowserConfig(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Errorf("remove catalog browser config: %v", err)
	}
}
