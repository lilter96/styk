//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/postgres"
)

func TestMoySkladSettingsBrowser(t *testing.T) {
	if os.Getenv("STYK_SETTINGS_BROWSER_E2E") != "1" {
		t.Skip("settings browser test not requested")
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/styk_settings_browser_test" {
		t.Fatal("dedicated local settings test database required")
	}
	db, err := postgres.Open(context.Background(), u.String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Close()
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	t.Setenv("STYK_SETTINGS_TEST_ACCOUNT", fmt.Sprintf("00000000-0000-4000-8000-%012x", time.Now().UnixNano()&0xffffffffffff))
	for _, key := range []string{"MOYSKLAD_TOKEN", "MOYSKLAD_ACCOUNT_ID", "OZON_CLIENT_ID", "OZON_API_KEY", "STYK_TEST_ACCOUNT_CATALOG", "STYK_TEST_ACCOUNT_PUBLICATION"} {
		t.Setenv(key, "")
	}
	provider := httptest.NewServer(http.HandlerFunc(settingsFakeProvider))
	defer provider.Close()
	mailbox := &testMailbox{tokens: map[string]string{}}
	handler, err := console.MerchantLoopbackTestHandler(db, "http://127.0.0.1:15179", mailbox, provider.URL, provider.URL)
	if err != nil {
		t.Fatal(err)
	}
	api := merchantBrowserAPI(t, db, mailbox, handler)
	defer api.Close()
	runSettingsBrowser(t, api.URL)
}

func settingsFakeProvider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer synthetic-settings-") {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/context/employee":
		_, err := io.WriteString(w, `{"accountId":"`+os.Getenv("STYK_SETTINGS_TEST_ACCOUNT")+`"}`)
		if err != nil {
			return
		}
	case "/entity/product":
		_, err := io.WriteString(w, `{"meta":{"size":1},"rows":[{"id":"00000000-0000-4000-8000-000000000046","accountId":"`+os.Getenv("STYK_SETTINGS_TEST_ACCOUNT")+`"}]}`)
		if err != nil {
			return
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func runSettingsBrowser(t *testing.T, apiURL string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	web, err := filepath.Abs("../../web")
	if err != nil {
		t.Fatal(err)
	}
	configPath := settingsViteConfig(t, web, apiURL)
	vite := exec.CommandContext(ctx, "node", "node_modules/vite/bin/vite.js", "--config", configPath)
	vite.Dir = web
	if err = vite.Start(); err != nil {
		t.Fatal(err)
	}
	defer stopSettingsVite(t, vite)
	waitMerchantVite(t, &bytes.Buffer{})
	cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "e2e/moysklad-settings.spec.ts", "--workers=1", "--reporter=line")
	cmd.Dir = web
	cmd.Env = append(os.Environ(), "STYK_SETTINGS_ORIGIN=http://127.0.0.1:15179", "STYK_SETTINGS_TEST_API="+apiURL)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("settings browser: %v\n%s", err, output)
	}
	t.Log(string(output))
}

func settingsViteConfig(t *testing.T, web, apiURL string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(web, "node_modules/.cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	config, err := os.CreateTemp(filepath.Join(web, "node_modules/.cache"), "settings-vite-*.mjs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(config.Name()); err != nil {
			t.Error(err)
		}
	})
	if _, err = fmt.Fprintf(config, `import {defineConfig} from 'vite'; export default defineConfig({server:{host:'127.0.0.1',port:15179,strictPort:true,proxy:{'/api':{target:%q,changeOrigin:false}}}});`, apiURL); err != nil {
		t.Fatal(err)
	}
	if err = config.Close(); err != nil {
		t.Fatal(err)
	}
	return config.Name()
}

func stopSettingsVite(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Error(err)
	}
	if err := cmd.Wait(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Error(err)
		}
	}
}
