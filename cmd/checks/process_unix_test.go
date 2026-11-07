//go:build unix

package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// The grandchild holds a TCP listener. Cancellation must release that listener,
// which killing only the shell/go parent would leave running indefinitely.
func TestCancellationStopsOwnedGrandchild(t *testing.T) {
	t.Parallel()
	assertOwnedGrandchildStops(t, false)
}

func TestCancellationStopsOwnedGrandchildAfterStdoutClosed(t *testing.T) {
	t.Parallel()
	assertOwnedGrandchildStops(t, true)
}

func TestNormalParentExitAlsoStopsOwnedGrandchild(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", `"$1" '-test.run=^TestOwnedListenerHelper$' &`, "checks-helper", os.Args[0])
	cmd.Env = append(cleanEnvironment(), "CHECKS_LISTENER_HELPER=1", "CHECKS_LISTENER_CLOSE_STDOUT=1")
	configureProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := finishProcess(cmd); err != nil {
			t.Error(err)
		}
	})
	port, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err != nil {
		t.Fatal("normal parent did not exit successfully", err)
	}
	if err = finishProcess(cmd); err != nil {
		t.Fatal(err)
	}
	assertPortReleased(t, port)
}

func assertOwnedGrandchildStops(t *testing.T, closeStdout bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", `"$1" '-test.run=^TestOwnedListenerHelper$' & wait`, "checks-helper", os.Args[0])
	cmd.Env = append(cleanEnvironment(), "CHECKS_LISTENER_HELPER=1")
	if closeStdout {
		cmd.Env = append(cmd.Env, "CHECKS_LISTENER_CLOSE_STDOUT=1")
	}
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
	signal.Ignore(syscall.SIGTERM)
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
	if os.Getenv("CHECKS_LISTENER_CLOSE_STDOUT") == "1" {
		if err = os.Stdout.Close(); err != nil {
			t.Fatal(err)
		}
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
