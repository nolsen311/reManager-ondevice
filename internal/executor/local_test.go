//go:build unix

package executor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"reManager/internal/component"
)

func TestLocalExecuteWithOutput(t *testing.T) {
	e := NewLocalExecutor(context.Background())

	out, err := e.ExecuteWithOutput("echo out; echo err >&2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "out\nerr\n" {
		t.Errorf("stdout and stderr should be combined in order, got %q", out)
	}
}

func TestLocalExecuteWithOutputNonZeroExit(t *testing.T) {
	e := NewLocalExecutor(context.Background())

	out, err := e.ExecuteWithOutput("echo before; exit 3")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("want exit status 3, got %v", err)
	}
	if out != "before\n" {
		t.Errorf("output before the failure should be kept, got %q", out)
	}
}

func TestLocalUsesDevicePath(t *testing.T) {
	e := NewLocalExecutor(context.Background())

	out, err := e.ExecuteWithOutput(`echo "$PATH"`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, devicePath+":") {
		t.Errorf("PATH should start with the device path, got %q", out)
	}
}

func TestLocalDropsLDPreload(t *testing.T) {
	// A path that does not exist: the loader warns and carries on, so the test's own shell still runs if the variable leaks through.
	t.Setenv("LD_PRELOAD", "/nonexistent/xovi.so")
	e := NewLocalExecutor(context.Background())

	out, err := e.ExecuteWithOutput(`echo "preload=[$LD_PRELOAD]"`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "preload=[]") {
		t.Errorf("LD_PRELOAD should not reach commands, got %q", out)
	}
}

func TestLocalExecuteStreaming(t *testing.T) {
	e := NewLocalExecutor(context.Background())

	var lines []string
	err := e.ExecuteStreaming(`echo one; echo two >&2; printf 'crlf\r\n'; printf 'no newline'`, func(line string) {
		lines = append(lines, line)
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(lines)
	want := []string{"crlf", "no newline", "one", "two"}
	if !slices.Equal(lines, want) {
		t.Errorf("got %q, want %q", lines, want)
	}
}

func TestLocalExecuteStreamingLongLine(t *testing.T) {
	e := NewLocalExecutor(context.Background())

	var lines []string
	err := e.ExecuteStreaming(`head -c 200000 /dev/zero | tr '\0' x; echo; echo after`, func(line string) {
		lines = append(lines, line)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || len(lines[0]) != 200000 || lines[1] != "after" {
		t.Errorf("a long line should arrive whole and not stop the stream, got %d lines", len(lines))
	}
}

func TestLocalExecuteStreamingNilCallback(t *testing.T) {
	e := NewLocalExecutor(context.Background())

	if err := e.ExecuteStreaming("echo ignored", nil); err != nil {
		t.Fatal(err)
	}
}

func TestLocalExecuteStreamingNonZeroExit(t *testing.T) {
	e := NewLocalExecutor(context.Background())

	var lines []string
	err := e.ExecuteStreaming("echo shown; exit 7", func(line string) { lines = append(lines, line) })
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("want exit status 7, got %v", err)
	}
	if !slices.Equal(lines, []string{"shown"}) {
		t.Errorf("got %q", lines)
	}
}

func TestLocalBackgroundChildDoesNotHang(t *testing.T) {
	e := NewLocalExecutor(context.Background())

	start := time.Now()
	out, err := e.ExecuteWithOutput("sleep 30 & echo started")
	if err != nil {
		t.Fatalf("a clean exit with a child left running is not a failure: %v", err)
	}
	if out != "started\n" {
		t.Errorf("got %q", out)
	}
	if elapsed := time.Since(start); elapsed > localWaitDelay+5*time.Second {
		t.Errorf("waited %s for a background child", elapsed)
	}
}

func TestLocalCancelKillsProcessGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithCancel(context.Background())
	e := NewLocalExecutor(ctx)

	var once sync.Once
	done := make(chan error, 1)
	go func() {
		// The sleep is a child of the shell, so it only dies if the whole group is killed.
		done <- e.ExecuteStreaming("sleep 60 & echo $! > "+QuoteArg(pidFile)+"; echo ready; wait", func(string) {
			once.Do(cancel)
		})
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled command should return an error")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("command did not stop after cancel")
	}

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	for _, c := range strings.TrimSpace(string(raw)) {
		pid = pid*10 + int(c-'0')
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("child %d survived the cancel", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLocalExecuteRunsInOrderAndStopsOnFailure(t *testing.T) {
	var lines []string
	e := NewLocalExecutor(context.Background())
	e.Output = func(line string) { lines = append(lines, line) }

	err := e.Execute([]component.CommandResult{
		{Script: "echo first"},
		{Script: "exit 1"},
		{Script: "echo never"},
	})
	if err == nil {
		t.Fatal("want the failing command's error")
	}
	want := []string{"$ echo first", "first", "$ exit 1"}
	if !slices.Equal(lines, want) {
		t.Errorf("got %q, want %q", lines, want)
	}
}

func TestLocalExecuteRefusesPTY(t *testing.T) {
	var lines []string
	e := NewLocalExecutor(context.Background())
	e.Output = func(line string) { lines = append(lines, line) }

	err := e.Execute([]component.CommandResult{{Script: "echo no", Description: "interactive step", RequiresPTY: true}})
	if err == nil || !strings.Contains(err.Error(), "interactive step") {
		t.Fatalf("want an error naming the step, got %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("the command should not have run, got %q", lines)
	}
}

func TestLocalCustomShell(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	e := NewLocalExecutor(context.Background())
	e.Shell = bash

	out, err := e.ExecuteWithOutput(`echo "${BASH_VERSION:+bash}"`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "bash" {
		t.Errorf("command did not run in the configured shell, got %q", out)
	}
}
