//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func (s *ordersProcessStack) browser(t *testing.T) {
	if os.Getenv("STYK_ORDERS_BROWSER_E2E") != "1" {
		t.Skip("orders browser stack not requested")
	}
	s.add(t, s.a, "browser-create", "FBS", false)
	s.workersEnabled(t, true)
	defer s.workersEnabled(t, false)
	proxy := ordersProcessProxy(t, s.apis)
	defer proxy.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	ordersProcessPlaywright(t, ctx, proxy.URL, s.a.scope)
	s.awaitState(t, "browser-create", orders.StateReconciled)
	if s.provider.writeCount("browser-create") != 1 {
		t.Fatal("browser approval did not create exactly one native customerorder")
	}
}

func ordersProcessProxy(t *testing.T, apis []*ordersProcessChild) *httptest.Server {
	t.Helper()
	proxies := make([]*httputil.ReverseProxy, 0, len(apis))
	for _, api := range apis {
		target, err := url.Parse(api.url)
		if err != nil {
			t.Fatal(err)
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ErrorLog = log.New(io.Discard, "", 0)
		proxies = append(proxies, proxy)
	}
	var next atomic.Uint64
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxies[(next.Add(1)-1)%uint64(len(proxies))].ServeHTTP(w, r)
	}))
}

func ordersProcessPlaywright(t *testing.T, ctx context.Context, proxy string, scope orders.Scope) {
	t.Helper()
	web, err := filepath.Abs("../../web")
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.CreateTemp(filepath.Join(web, "node_modules/.cache"), "orders-stack-vite-*.mjs")
	if err != nil {
		t.Fatal("create orders Vite fixture config")
	}
	t.Cleanup(func() {
		if err := os.Remove(config.Name()); err != nil {
			t.Error("remove orders Vite fixture config")
		}
	})
	_, err = fmt.Fprintf(config, `import {defineConfig} from 'vite'; export default defineConfig({server:{host:'127.0.0.1',port:15183,strictPort:true,proxy:{'/api':{target:%q,changeOrigin:false}}}});`, proxy)
	closeErr := config.Close()
	if err != nil || closeErr != nil {
		t.Fatal("write orders Vite fixture config")
	}
	vite := exec.CommandContext(ctx, "node", "node_modules/vite/bin/vite.js", "--config", config.Name())
	vite.Dir = web
	logs := &ordersProcessLog{}
	vite.Stdout, vite.Stderr = logs, logs
	if err = vite.Start(); err != nil {
		t.Fatal("start orders browser application")
	}
	defer ordersProcessStopCommand(t, vite)
	ordersProcessViteReady(t, logs)
	ordersProcessRunBrowser(t, ctx, web, scope)
}

func ordersProcessViteReady(t *testing.T, logs *ordersProcessLog) {
	t.Helper()
	ordersProcessEventually(t, 10*time.Second, "orders Vite browser application", func() bool {
		response, err := http.Get(ordersProcessOrigin)
		if err != nil {
			return false
		}
		if err = response.Body.Close(); err != nil {
			t.Error("close orders Vite readiness response")
		}
		return response.StatusCode == http.StatusOK
	})
	if t.Failed() {
		t.Log(logs.String())
	}
}

func ordersProcessRunBrowser(t *testing.T, ctx context.Context, web string, scope orders.Scope) {
	t.Helper()
	raw, err := json.Marshal(scope)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "e2e/orders-stack.spec.ts", "--workers=1", "--reporter=line")
	cmd.Dir = web
	cmd.Env = append(os.Environ(), "STYK_ORDERS_STACK_ORIGIN="+ordersProcessOrigin, "STYK_ORDERS_STACK_SCOPE="+string(raw))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("orders live-stack browser gate: %v\n%s", err, output)
	}
	t.Log(string(output))
}

func ordersProcessStopCommand(t *testing.T, command *exec.Cmd) {
	t.Helper()
	if err := command.Process.Kill(); err != nil {
		t.Error("stop orders browser application")
	}
	if err := command.Wait(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Error("wait for orders browser application")
		}
	}
}
