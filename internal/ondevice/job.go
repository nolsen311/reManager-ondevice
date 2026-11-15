//go:build linux

package ondevice

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// An install, removal or upgrade runs in a worker process outside the stock
// UI's service, because AppLoad starts the backend as a child of the stock UI
// and a restart of the stock UI would take an in-process job down with it. The worker writes
// what it does to a file; the backend, or a backend started later, reads it.

const (
	// DefaultStateDir is on the home partition, never the root partition.
	DefaultStateDir = "/home/root/.local/state/remanager-ondevice"

	jobUnit       = "remanager-ondevice-job"
	eventsFile    = "job.jsonl"
	lockFile      = "job.lock"
	maxOutputLine = 2000
)

// Colour codes in program output, such as xochitl's log lines.
var terminalCodes = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

func stripTerminalCodes(s string) string {
	return terminalCodes.ReplaceAllString(s, "")
}

// Event is one line of the worker's record of a job.
type Event struct {
	Seq  int    `json:"seq"`
	Kind string `json:"kind"`
	// Action and Packages are set on "started". An upgrade of everything has no packages.
	Action   string   `json:"action,omitempty"`
	Packages []string `json:"packages,omitempty"`
	// Progress is set on "progress".
	Progress *Progress `json:"progress,omitempty"`
	// Result is set on "done".
	Result *InstallResult `json:"result,omitempty"`
	// At is when the event was recorded, in Unix seconds.
	At int64 `json:"at,omitempty"`
}

const (
	EventStarted  = "started"
	EventProgress = "progress"
	EventDone     = "done"
)

// ErrJobRunning means a job is already in progress; Vellum is not safe to run twice at once.
var ErrJobRunning = errors.New("another install, removal or update is still running")

// Jobs starts installs, removals and upgrades in a worker process and reads back what they did.
type Jobs struct {
	// Dir holds the worker's lock and its record of the last job.
	Dir string
	// Launch starts the worker with the given arguments and returns once it is running. Nil means a transient systemd unit running this executable.
	Launch func(args []string) error
}

// JobStatus is the state of the most recent job.
type JobStatus struct {
	Running bool    `json:"running"`
	Events  []Event `json:"events"`
}

func (j *Jobs) dir() string {
	if j.Dir != "" {
		return j.Dir
	}
	return DefaultStateDir
}

// Only an upgrade may have no names: that means everything.
func checkJob(action string, names []string) error {
	switch action {
	case ActionInstall, ActionRemove:
		return checkNames(names)
	case ActionRestart, ActionRebuildHashtable:
		if len(names) > 0 {
			return fmt.Errorf("%s takes no packages", action)
		}
		return nil
	case ActionUpgrade:
		if len(names) == 0 {
			return nil
		}
		return checkNames(names)
	default:
		return fmt.Errorf("unknown action %q", action)
	}
}

// lock takes the job lock, which the kernel releases if the holder dies. It returns ErrJobRunning when another process holds it. The worker holds it exclusively; a check for a running worker takes it shared, so two checks never look like a worker to each other.
func (j *Jobs) lock(how int) (*os.File, error) {
	if err := os.MkdirAll(j.dir(), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(j.dir(), lockFile), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrJobRunning
		}
		return nil, err
	}
	return f, nil
}

func (j *Jobs) Running() bool {
	f, err := j.lock(syscall.LOCK_SH)
	if err != nil {
		return errors.Is(err, ErrJobRunning)
	}
	f.Close()
	return false
}

// Start launches a worker to run action on names and returns once it has been asked to start.
func (j *Jobs) Start(action string, names []string) error {
	if err := checkJob(action, names); err != nil {
		return err
	}
	if j.Running() {
		return ErrJobRunning
	}
	// The last job's record goes now, so a reader never mistakes it for this one's.
	if err := os.Remove(filepath.Join(j.dir(), eventsFile)); err != nil && !os.IsNotExist(err) {
		return err
	}
	args := append([]string{"worker", action, "--state", j.dir(), "--"}, names...)
	launch := j.Launch
	if launch == nil {
		launch = launchInSystemdUnit
	}
	return launch(args)
}

// --collect removes the unit when it ends, even on failure, so the fixed name is free for the next job.
func launchInSystemdUnit(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command("systemd-run", systemdRunArgs(exe, args)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("could not start the worker: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func systemdRunArgs(exe string, args []string) []string {
	runArgs := []string{"--unit=" + jobUnit, "--collect", "--quiet", "--setenv=HOME=/home/root"}
	if len(args) > 1 && isSystemAction(args[1]) {
		// However the job ends, systemd runs this after it, so the UI comes back even if the worker itself was killed.
		runArgs = append(runArgs, "--property=ExecStopPost="+exe+" worker restore-ui")
	}
	if len(args) > 1 && args[1] == ActionRebuildHashtable {
		// A rebuild normally takes one to two minutes; one that runs on is stuck on its temporary UI.
		runArgs = append(runArgs, "--property=RuntimeMaxSec=300")
	}
	return append(append(runArgs, exe), args...)
}

// RunWorker is the worker's side: it holds the lock for the whole job and records every step. run does the work and reports through the callback.
func (j *Jobs) RunWorker(action string, names []string, run func(names []string, onProgress func(Progress)) InstallResult) error {
	if err := checkJob(action, names); err != nil {
		return err
	}
	// A backend checking for a running worker holds the lock for an instant, so one refusal does not mean another worker.
	var lock *os.File
	var err error
	for attempt := 0; ; attempt++ {
		lock, err = j.lock(syscall.LOCK_EX)
		if !errors.Is(err, ErrJobRunning) || attempt >= 20 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	defer lock.Close()

	f, err := os.OpenFile(filepath.Join(j.dir(), eventsFile), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	seq := 0
	var lastSync time.Time
	write := func(e Event) {
		seq++
		e.Seq = seq
		e.At = time.Now().Unix()
		line, _ := json.Marshal(e)
		f.Write(append(line, '\n'))
		// The stock UI may be restarted, or the tablet rebooted, at any point in a job, so the start and the result reach the disk at once. Progress can be chatty, so it is flushed at most once a second.
		if e.Kind != EventProgress || time.Since(lastSync) > time.Second {
			f.Sync()
			lastSync = time.Now()
		}
	}

	write(Event{Kind: EventStarted, Action: action, Packages: names})
	result := run(names, func(p Progress) {
		p.Message = stripTerminalCodes(p.Message)
		if len(p.Message) > maxOutputLine {
			p.Message = p.Message[:maxOutputLine]
		}
		write(Event{Kind: EventProgress, Progress: &p})
	})
	for i, message := range result.Errors {
		result.Errors[i] = withOfflineHint(message)
	}
	write(Event{Kind: EventDone, Result: &result})
	return nil
}

// Events returns the recorded events with a sequence number above after.
func (j *Jobs) Events(after int) ([]Event, error) {
	f, err := os.Open(filepath.Join(j.dir(), eventsFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var e Event
		// A line the worker is still writing does not parse yet; it is picked up on the next read.
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			break
		}
		if e.Seq > after {
			events = append(events, e)
		}
	}
	return events, scanner.Err()
}

// Status returns the last job in brief: its start, its latest progress and its result. The full record can run to thousands of lines (a hashtable rebuild records everything the temporary UI prints), far more than one AppLoad message carries.
func (j *Jobs) Status() (JobStatus, error) {
	running := j.Running()
	events, err := j.Events(0)
	return JobStatus{Running: running, Events: summarizeEvents(events)}, err
}

func summarizeEvents(events []Event) []Event {
	var started, progress, done *Event
	for i := range events {
		switch events[i].Kind {
		case EventStarted:
			started = &events[i]
		case EventProgress:
			progress = &events[i]
		case EventDone:
			done = &events[i]
		}
	}
	summary := []Event{}
	for _, e := range []*Event{started, progress, done} {
		if e != nil {
			summary = append(summary, *e)
		}
	}
	return summary
}

// Follow reports events after the given sequence number as the worker records them, and returns when the job is done, the worker has gone, or ctx ends.
func (j *Jobs) Follow(ctx context.Context, after int, onEvent func(Event)) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	idle := 0
	for {
		// Read whether the worker is alive before reading its record, so its last events are not missed if it ends in between.
		running := j.Running()
		events, err := j.Events(after)
		if err != nil {
			return err
		}
		for _, e := range events {
			after = e.Seq
			onEvent(e)
			if e.Kind == EventDone {
				return nil
			}
		}
		if running || len(events) > 0 {
			idle = 0
		} else if idle++; idle > 20 {
			// Long enough for a worker that was just launched to take the lock.
			return fmt.Errorf("the job stopped without reporting a result")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
