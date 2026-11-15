//go:build unix

package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"reManager/internal/component"
)

// A command that leaves a child holding its output open would otherwise keep the caller waiting after the command itself has exited.
const localWaitDelay = 2 * time.Second

// LocalExecutor runs commands on the machine it is running on, for the build of reManager that lives on the tablet.
type LocalExecutor struct {
	// Shell runs each command as `Shell -c command`; empty means /bin/sh.
	Shell string
	// Output receives what the commands given to Execute print, and a "$ script" line before each one. It is never called from two goroutines at once.
	Output func(line string)

	ctx context.Context
}

var _ CommandExecutor = (*LocalExecutor)(nil)

// Cancelling ctx kills every running command along with the processes it started.
func NewLocalExecutor(ctx context.Context) *LocalExecutor {
	return &LocalExecutor{ctx: ctx}
}

// Err reports why the executor's context ended, or nil while it is still live.
func (e *LocalExecutor) Err() error {
	if e.ctx == nil {
		return nil
	}
	return e.ctx.Err()
}

func (e *LocalExecutor) Execute(commands []component.CommandResult) error {
	for _, c := range commands {
		if c.RequiresPTY {
			return fmt.Errorf("%q needs a terminal, which the on-device executor does not provide", c.Description)
		}
		if e.Output != nil {
			e.Output("$ " + c.Script)
		}
		if err := e.ExecuteStreaming(c.Script, e.Output); err != nil {
			return err
		}
	}
	return nil
}

func (e *LocalExecutor) ExecuteWithOutput(cmd string) (string, error) {
	var out bytes.Buffer
	c := e.command(cmd)
	c.Stdout = &out
	c.Stderr = &out
	err := e.run(c)
	return out.String(), err
}

func (e *LocalExecutor) ExecuteStreaming(cmd string, onOutput func(line string)) error {
	var mu sync.Mutex
	stdout := &lineWriter{mu: &mu, emit: onOutput}
	stderr := &lineWriter{mu: &mu, emit: onOutput}
	c := e.command(cmd)
	c.Stdout = stdout
	c.Stderr = stderr
	err := e.run(c)
	stdout.flush()
	stderr.flush()
	return err
}

func (e *LocalExecutor) command(cmd string) *exec.Cmd {
	shell := e.Shell
	if shell == "" {
		shell = "/bin/sh"
	}
	ctx := e.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	c := exec.CommandContext(ctx, shell, "-c", WithDevicePath(cmd))
	c.Env = localEnv(os.Environ())
	// Its own process group, so cancelling reaches what the shell started and not only the shell.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	c.WaitDelay = localWaitDelay
	return c
}

func (e *LocalExecutor) run(c *exec.Cmd) error {
	err := c.Run()
	// The command exited cleanly and only a leftover child kept the output open.
	if errors.Is(err, exec.ErrWaitDelay) {
		return nil
	}
	return err
}

// A backend started from xochitl inherits the LD_PRELOAD that loads xovi into it; commands run over SSH never had it, so they do not get it here either.
func localEnv(environ []string) []string {
	env := make([]string, 0, len(environ))
	for _, kv := range environ {
		if strings.HasPrefix(kv, "LD_PRELOAD=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// lineWriter turns a byte stream into whole lines. mu is shared between a command's stdout and stderr so their lines never interleave inside emit.
type lineWriter struct {
	mu   *sync.Mutex
	emit func(line string)
	buf  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.send(w.buf[:i])
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.send(w.buf)
		w.buf = nil
	}
}

func (w *lineWriter) send(line []byte) {
	if w.emit != nil {
		w.emit(strings.TrimSuffix(string(line), "\r"))
	}
}
