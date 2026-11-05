//go:build unix

package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"
)

// The grandchild holds a TCP listener. Cancellation must release that listener,
// which killing only the shell/go parent would leave running indefinitely.
func TestCancellationStopsOwnedGrandchild(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", `"$1" '-test.run=^TestOwnedListenerHelper$' & wait`, "checks-helper", os.Args[0])
	cmd.Env = append(cleanEnvironment(), "CHECKS_LISTENER_HELPER=1")
	configureProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	port, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		cancel()
		waitErr := cmd.Wait()
		t.Fatalf("read listener: %v; stopped process: %v", err, waitErr)
	}
	cancel()
	if err = cmd.Wait(); err == nil {
		t.Fatal("cancelled check reported success")
	}
	assertPortReleased(t, port)
}

func TestOwnedListenerHelper(t *testing.T) {
	if os.Getenv("CHECKS_LISTENER_HELPER") != "1" {
		t.Skip("owned cancellation subprocess only")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err = fmt.Fprintln(os.Stdout, listener.Addr().(*net.TCPAddr).Port); err != nil {
		t.Fatal(err)
	}
	<-time.After(time.Minute)
	t.Fatal("parent did not cancel owned listener within one minute")
}

func assertPortReleased(t *testing.T, port string) {
	t.Helper()
	address := "127.0.0.1:" + port[:len(port)-1]
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		listener, err := net.Listen("tcp", address)
		if err == nil {
			if err = listener.Close(); err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cancellation leaked a grandchild listener")
}
