//go:build linux

package ondevice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"reManager/internal/appload"
)

// Message types between the QML frontend and the backend. Contents are JSON.
const (
	// MsgRequest is frontend to backend: {"id": 1, "method": "list", "params": {...}}.
	MsgRequest int32 = 1
	// MsgResponse is backend to frontend: {"id": 1, "result": {...}} or {"id": 1, "error": "..."}.
	MsgResponse int32 = 2
	// MsgInstallEvent is backend to frontend, unprompted: one Event of the running job.
	MsgInstallEvent int32 = 3
	// MsgReady is backend to frontend, with no contents, each time a frontend attaches. AppLoad drops what a frontend sends before the backend has connected, so the frontend waits for this before its first request.
	MsgReady int32 = 4
)

// Transport is the AppLoad connection, or a stand-in for it in tests.
type Transport interface {
	Send(msgType int32, contents string) error
	Receive() (appload.Message, error)
}

type request struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type response struct {
	ID     int    `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type namesParams struct {
	Names []string `json:"names"`
}

// Handler answers the frontend's requests.
type Handler struct {
	Service *Service
	Jobs    *Jobs

	followMu  sync.Mutex
	following bool
}

type deviceInfo struct {
	Model    string `json:"model"`
	Name     string `json:"name"`
	Firmware string `json:"firmware"`
	Tested   bool   `json:"tested"`
	// AppVersion is this app's own version.
	AppVersion string `json:"appVersion"`
}

// Serve answers requests until AppLoad closes the connection or asks the backend to stop.
func (h *Handler) Serve(ctx context.Context, t Transport) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for {
		msg, err := t.Receive()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch msg.Type {
		case appload.TypeTerminate:
			return nil
		case appload.TypeFrontendAttached:
			t.Send(MsgReady, "")
			// A frontend that opens during a job, or after the stock UI restarted, picks the job back up.
			if h.Jobs.Running() {
				h.resume(ctx, t)
			}
		case MsgRequest:
			// Requests run one at a time: Vellum must not run twice at once, and the frontend shows one screen at a time.
			h.handle(ctx, t, msg.Contents)
		}
	}
}

func (h *Handler) handle(ctx context.Context, t Transport, contents string) {
	var req request
	if err := json.Unmarshal([]byte(contents), &req); err != nil {
		reply(t, response{Error: "could not read the request: " + err.Error()})
		return
	}
	result, err := h.call(ctx, t, req)
	if err != nil {
		reply(t, response{ID: req.ID, Error: withOfflineHint(err.Error())})
		return
	}
	if plan, ok := result.(*Plan); ok {
		plan.Error = withOfflineHint(plan.Error)
	}
	reply(t, response{ID: req.ID, Result: result})
}

func reply(t Transport, resp response) {
	data, err := json.Marshal(resp)
	if err != nil {
		data, _ = json.Marshal(response{ID: resp.ID, Error: "could not encode the reply: " + err.Error()})
	}
	// A reply that cannot be sent, most often because it is too big for one message, would otherwise leave the frontend waiting.
	if err := t.Send(MsgResponse, string(data)); err != nil {
		fallback, _ := json.Marshal(response{ID: resp.ID, Error: fmt.Sprintf("the reply could not be sent (%d bytes): %v", len(data), err)})
		t.Send(MsgResponse, string(fallback))
	}
}

func (h *Handler) call(ctx context.Context, t Transport, req request) (any, error) {
	params := func(v any) error {
		if len(req.Params) == 0 {
			return nil
		}
		if err := json.Unmarshal(req.Params, v); err != nil {
			return fmt.Errorf("could not read the parameters of %q: %w", req.Method, err)
		}
		return nil
	}

	switch req.Method {
	case "device":
		d := h.Service.device
		return deviceInfo{Model: string(d.Type), Name: d.Type.DisplayName(), Firmware: h.Service.firmware(), Tested: d.Tested(), AppVersion: Version}, nil
	case "refresh":
		return struct{}{}, h.Service.Refresh()
	case "list":
		var p ListRequest
		if err := params(&p); err != nil {
			return nil, err
		}
		return h.Service.List(p)
	case "detail":
		var p struct {
			Name string `json:"name"`
		}
		if err := params(&p); err != nil {
			return nil, err
		}
		return h.Service.Detail(p.Name)
	case "plan":
		var p namesParams
		if err := params(&p); err != nil {
			return nil, err
		}
		return h.Service.Plan(p.Names)
	case "planRemove":
		var p namesParams
		if err := params(&p); err != nil {
			return nil, err
		}
		return h.Service.PlanRemove(p.Names)
	case "upgrades":
		return h.Service.Upgrades()
	case "planUpgrade":
		var p namesParams
		if err := params(&p); err != nil {
			return nil, err
		}
		return h.Service.PlanUpgrade(p.Names)
	case "system":
		return h.Service.SystemStatus(), nil
	case "checks":
		return h.Service.Checks(h.Jobs.Running()), nil
	case "resumeTripletap":
		return struct{}{}, h.Service.ResumeTripletap()
	case ActionInstall, ActionRemove, ActionUpgrade, ActionRestart, ActionRebuildHashtable:
		var p namesParams
		if err := params(&p); err != nil {
			return nil, err
		}
		if err := h.Jobs.Start(req.Method, p.Names); err != nil {
			return nil, err
		}
		h.follow(ctx, t, 0)
		return struct{}{}, nil
	case "job":
		return h.Jobs.Status()
	default:
		return nil, fmt.Errorf("unknown method %q", req.Method)
	}
}

// resume brings a newly attached frontend up to date on a running job in brief, then follows it from there.
func (h *Handler) resume(ctx context.Context, t Transport) {
	events, err := h.Jobs.Events(0)
	if err != nil {
		return
	}
	after := 0
	for _, e := range summarizeEvents(events) {
		if data, err := json.Marshal(e); err == nil {
			t.Send(MsgInstallEvent, string(data))
		}
		after = e.Seq
	}
	if len(events) > 0 {
		after = events[len(events)-1].Seq
	}
	h.follow(ctx, t, after)
}

// follow forwards the job's events to the frontend in the background. One follower at a time, so an event is never sent twice.
func (h *Handler) follow(ctx context.Context, t Transport, after int) {
	h.followMu.Lock()
	if h.following {
		h.followMu.Unlock()
		return
	}
	h.following = true
	h.followMu.Unlock()

	go func() {
		defer func() {
			h.followMu.Lock()
			h.following = false
			h.followMu.Unlock()
		}()
		err := h.Jobs.Follow(ctx, after, func(e Event) {
			if data, err := json.Marshal(e); err == nil {
				t.Send(MsgInstallEvent, string(data))
			}
		})
		if err != nil && ctx.Err() == nil {
			data, _ := json.Marshal(Event{Kind: EventDone, Result: &InstallResult{Errors: []string{err.Error()}}})
			t.Send(MsgInstallEvent, string(data))
		}
	}()
}
