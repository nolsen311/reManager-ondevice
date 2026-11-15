//go:build linux

package ondevice

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reManager/internal/appload"
)

// inProcessJobs runs the worker in a goroutine instead of a systemd unit. release, when set, holds the install open until the test lets it finish.
func inProcessJobs(t *testing.T, result InstallResult, release chan struct{}) *Jobs {
	t.Helper()
	jobs := &Jobs{Dir: t.TempDir()}
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	jobs.Launch = func(args []string) error {
		worker := &Jobs{Dir: args[3]}
		names := args[5:]
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker.RunWorker(names, func(names []string, onProgress func(Progress)) InstallResult {
				onProgress(Progress{Package: names[0], Index: 0, Total: 1, Status: "installing", Message: "Installing " + names[0]})
				if release != nil {
					<-release
				}
				return result
			})
		}()
		return nil
	}
	return jobs
}

func kinds(events []Event) []string {
	var out []string
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
}

func TestJobRecordsAndFollows(t *testing.T) {
	jobs := inProcessJobs(t, InstallResult{Success: true, Deferred: []string{"Build hashtable"}}, nil)

	if err := jobs.StartInstall([]string{"koreader"}); err != nil {
		t.Fatal(err)
	}
	var events []Event
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := jobs.Follow(ctx, 0, func(e Event) { events = append(events, e) }); err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(kinds(events), ","); got != "started,progress,done" {
		t.Fatalf("events = %s", got)
	}
	for i, e := range events {
		if e.Seq != i+1 {
			t.Errorf("event %d has seq %d", i, e.Seq)
		}
	}
	done := events[2].Result
	if done == nil || !done.Success || len(done.Deferred) != 1 {
		t.Errorf("result = %+v", done)
	}

	// A backend started after the install, as happens when the stock UI restarts, still sees what happened.
	later := &Jobs{Dir: jobs.Dir}
	status, err := later.Status()
	if err != nil || status.Running || len(status.Events) != 3 {
		t.Errorf("status = %+v, err %v", status, err)
	}
	after, _ := later.Events(2)
	if len(after) != 1 || after[0].Kind != EventDone {
		t.Errorf("events after 2 = %+v", after)
	}
}

func TestJobRefusesASecondInstall(t *testing.T) {
	release := make(chan struct{})
	jobs := inProcessJobs(t, InstallResult{Success: true}, release)
	defer close(release)

	if err := jobs.StartInstall([]string{"one"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !jobs.Running() {
		if time.Now().After(deadline) {
			t.Fatal("worker never took the lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := jobs.StartInstall([]string{"two"}); !errors.Is(err, ErrJobRunning) {
		t.Errorf("want ErrJobRunning, got %v", err)
	}
	events, _ := jobs.Events(0)
	if len(events) == 0 || events[0].Packages[0] != "one" {
		t.Errorf("the refused install must not disturb the running one's record, got %+v", events)
	}
}

func TestJobStartRejectsBadNamesBeforeLaunching(t *testing.T) {
	jobs := &Jobs{Dir: t.TempDir(), Launch: func([]string) error {
		t.Error("worker launched")
		return nil
	}}
	if err := jobs.StartInstall([]string{"$(reboot)"}); err == nil {
		t.Error("want an error")
	}
}

func TestFollowReportsAWorkerThatDied(t *testing.T) {
	jobs := &Jobs{Dir: t.TempDir()}
	line, _ := json.Marshal(Event{Seq: 1, Kind: EventStarted, Packages: []string{"koreader"}})
	if err := os.WriteFile(filepath.Join(jobs.Dir, eventsFile), append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var seen int
	err := jobs.Follow(ctx, 0, func(Event) { seen++ })
	if err == nil || ctx.Err() != nil {
		t.Fatalf("want an error saying the install stopped, got %v", err)
	}
	if seen != 1 {
		t.Errorf("saw %d events, want the one recorded", seen)
	}
}

func TestEventsIgnoresAHalfWrittenLine(t *testing.T) {
	jobs := &Jobs{Dir: t.TempDir()}
	line, _ := json.Marshal(Event{Seq: 1, Kind: EventStarted})
	os.WriteFile(filepath.Join(jobs.Dir, eventsFile), append(append(line, '\n'), []byte(`{"seq":2,"kind":"prog`)...), 0o644)

	events, err := jobs.Events(0)
	if err != nil || len(events) != 1 {
		t.Errorf("events = %+v, err %v", events, err)
	}
}

// fakeFrontend plays AppLoad and the QML frontend.
type fakeFrontend struct {
	in  chan appload.Message
	out chan appload.Message
}

func newFakeFrontend() *fakeFrontend {
	return &fakeFrontend{in: make(chan appload.Message, 16), out: make(chan appload.Message, 64)}
}

func (f *fakeFrontend) Send(msgType int32, contents string) error {
	f.out <- appload.Message{Type: msgType, Contents: contents}
	return nil
}

func (f *fakeFrontend) Receive() (appload.Message, error) {
	msg, ok := <-f.in
	if !ok {
		return appload.Message{}, io.EOF
	}
	return msg, nil
}

func (f *fakeFrontend) next(t *testing.T) appload.Message {
	t.Helper()
	select {
	case msg := <-f.out:
		return msg
	case <-time.After(10 * time.Second):
		t.Fatal("no message from the backend")
		return appload.Message{}
	}
}

func (f *fakeFrontend) request(t *testing.T, contents string) map[string]any {
	t.Helper()
	f.in <- appload.Message{Type: MsgRequest, Contents: contents}
	msg := f.next(t)
	if msg.Type != MsgResponse {
		t.Fatalf("want a response, got type %d: %s", msg.Type, msg.Contents)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(msg.Contents), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func serve(t *testing.T, h *Handler) *fakeFrontend {
	t.Helper()
	f := newFakeFrontend()
	done := make(chan error, 1)
	go func() { done <- h.Serve(context.Background(), f) }()
	t.Cleanup(func() {
		close(f.in)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Serve did not return when the connection closed")
		}
	})
	return f
}

func TestHandlerAnswersRequests(t *testing.T) {
	tablet := standardTablet().on("add --simulate", "(1/1) Installing koreader (1-r0)\n", nil)
	h := &Handler{Service: newTestService(standardCatalog(), tablet), Jobs: &Jobs{Dir: t.TempDir()}}
	f := serve(t, h)

	resp := f.request(t, `{"id":1,"method":"device"}`)
	device := resp["result"].(map[string]any)
	if resp["id"].(float64) != 1 || device["model"] != "rmppmove" || device["firmware"] != "3.27.3.0" || device["tested"] != true {
		t.Errorf("device = %v", resp)
	}

	resp = f.request(t, `{"id":2,"method":"list","params":{"query":"ebook"}}`)
	list := resp["result"].(map[string]any)
	if list["total"].(float64) != 1 {
		t.Errorf("list = %v", resp)
	}

	resp = f.request(t, `{"id":3,"method":"plan","params":{"names":["koreader"]}}`)
	if got := resp["result"].(map[string]any)["packages"].([]any); len(got) != 1 || got[0] != "koreader" {
		t.Errorf("plan = %v", resp)
	}

	resp = f.request(t, `{"id":4,"method":"detail","params":{"name":"missing"}}`)
	if resp["error"] == nil || resp["id"].(float64) != 4 {
		t.Errorf("want an error reply carrying the request id, got %v", resp)
	}

	resp = f.request(t, `{"id":5,"method":"reboot"}`)
	if resp["error"] == nil {
		t.Errorf("want an error for an unknown method, got %v", resp)
	}

	resp = f.request(t, `not json`)
	if resp["error"] == nil {
		t.Errorf("want an error for a malformed request, got %v", resp)
	}
}

func TestHandlerInstallStreamsEvents(t *testing.T) {
	jobs := inProcessJobs(t, InstallResult{Success: true}, nil)
	h := &Handler{Service: newTestService(standardCatalog(), standardTablet()), Jobs: jobs}
	f := serve(t, h)

	resp := f.request(t, `{"id":1,"method":"install","params":{"names":["koreader"]}}`)
	if resp["error"] != nil {
		t.Fatalf("install = %v", resp)
	}

	var got []string
	for len(got) < 3 {
		msg := f.next(t)
		if msg.Type != MsgInstallEvent {
			t.Fatalf("want install events, got type %d: %s", msg.Type, msg.Contents)
		}
		var e Event
		json.Unmarshal([]byte(msg.Contents), &e)
		got = append(got, e.Kind)
	}
	if strings.Join(got, ",") != "started,progress,done" {
		t.Errorf("events = %v", got)
	}

	resp = f.request(t, `{"id":2,"method":"job"}`)
	job := resp["result"].(map[string]any)
	if job["running"] != false || len(job["events"].([]any)) != 3 {
		t.Errorf("job = %v", resp)
	}
}

func TestHandlerResumesAnInstallWhenAFrontendAttaches(t *testing.T) {
	release := make(chan struct{})
	jobs := inProcessJobs(t, InstallResult{Success: true}, release)
	if err := jobs.StartInstall([]string{"koreader"}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); !jobs.Running(); {
		if time.Now().After(deadline) {
			t.Fatal("worker never took the lock")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A new backend, as after a restart of the stock UI, with the install still going.
	h := &Handler{Service: newTestService(standardCatalog(), standardTablet()), Jobs: &Jobs{Dir: jobs.Dir}}
	f := serve(t, h)
	f.in <- appload.Message{Type: appload.TypeFrontendAttached, Contents: "1"}

	var first Event
	json.Unmarshal([]byte(f.next(t).Contents), &first)
	if first.Kind != EventStarted {
		t.Errorf("want the install replayed from its start, got %+v", first)
	}
	close(release)
	for {
		var e Event
		json.Unmarshal([]byte(f.next(t).Contents), &e)
		if e.Kind == EventDone {
			break
		}
	}
}

func TestHandlerStopsOnTerminate(t *testing.T) {
	h := &Handler{Service: newTestService(standardCatalog(), standardTablet()), Jobs: &Jobs{Dir: t.TempDir()}}
	f := newFakeFrontend()
	done := make(chan error, 1)
	go func() { done <- h.Serve(context.Background(), f) }()

	f.in <- appload.Message{Type: appload.TypeTerminate, Contents: "close"}
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve kept running after terminate")
	}
}
