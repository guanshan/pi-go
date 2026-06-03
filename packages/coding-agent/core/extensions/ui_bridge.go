package extensions

import (
	"context"
	"encoding/json"
)

// UIRequestHandler answers a server-initiated UI request from a script
// extension's ctx.ui (select/confirm/input/notify). method is the ui method name
// and params is the raw JSON the extension passed; the returned JSON is the value
// the extension's await resolves to (e.g. the chosen string for "select", a bool
// for "confirm"). An error rejects the extension's promise. Hosts bind a handler
// via API.SetUIHandler — the interactive TUI shows an overlay, the RPC mode
// forwards to its client; when no handler is bound the bridge replies with an
// error so a UI-gated extension fails loudly instead of taking a wrong branch.
type UIRequestHandler func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)

// SetUIHandler binds (or clears, with nil) the host's UI request handler. Safe to
// call after extensions have loaded; the bridge resolves the handler at request
// time and pushes the new ctx.hasUI capability (handler != nil) to every loaded
// extension so ctx.hasUI tracks the handler, matching the TS live getter.
func (api *API) SetUIHandler(handler UIRequestHandler) {
	if api == nil {
		return
	}
	// Stamp a monotonic sequence under the lock and snapshot the listeners, then
	// notify them off the lock so a slow/blocking listener can never stall the
	// caller (SetUIHandler runs on the host's command goroutine). The sequence
	// lets each listener resolve a true/false race to the latest state even though
	// the dispatch goroutines may run out of order.
	api.mu.Lock()
	api.uiHandler = handler
	api.uiSeq++
	seq := api.uiSeq
	has := handler != nil
	listeners := make([]func(uint64, bool), len(api.uiListeners))
	copy(listeners, api.uiListeners)
	api.mu.Unlock()
	for _, listen := range listeners {
		go listen(seq, has)
	}
}

// registerUIListener registers a callback invoked (outside the API lock, on a
// dispatch goroutine) whenever the UI handler is bound or cleared, with the
// SetUIHandler sequence and the new ctx.hasUI capability. Each script runtime
// registers one to forward set_has_ui to its extension.
func (api *API) registerUIListener(listen func(uint64, bool)) {
	if api == nil || listen == nil {
		return
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	api.uiListeners = append(api.uiListeners, listen)
}

// sendSetHasUI records the latest ctx.hasUI capability for the extension and
// wakes hasUIWriteLoop to push it. It never blocks the caller: seq discards
// stale notifications so an out-of-order dispatch goroutine can't overwrite a
// newer state, and the wake is a non-blocking signal. The actual (potentially
// blocking) stdin write happens on the runtime's own goroutine, so a stuck
// extension can never stall the host or leak a goroutine per binding.
func (r *scriptRuntime) sendSetHasUI(seq uint64, has bool) {
	if r == nil {
		return
	}
	r.hasUIMu.Lock()
	if seq <= r.hasUISeq {
		r.hasUIMu.Unlock()
		return
	}
	r.hasUISeq = seq
	r.hasUIPending = has
	r.hasUIMu.Unlock()
	select {
	case r.hasUIWake <- struct{}{}:
	default:
	}
}

// hasUIWriteLoop is the sole writer of set_has_ui frames. It wakes on a pending
// signal, writes the latest recorded state, and exits when the runtime context
// is cancelled (Shutdown). Writing the latest state (rather than each transition)
// coalesces a rapid true/false sequence to its final value.
func (r *scriptRuntime) hasUIWriteLoop() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.hasUIWake:
			r.hasUIMu.Lock()
			has := r.hasUIPending
			r.hasUIMu.Unlock()
			r.writeStdinLine(map[string]any{"type": "set_has_ui", "value": has})
		}
	}
}

// UIHandler returns the currently bound UI request handler, or nil.
func (api *API) UIHandler() UIRequestHandler {
	if api == nil {
		return nil
	}
	api.mu.RLock()
	defer api.mu.RUnlock()
	return api.uiHandler
}

// uiRequestMessage is a server-initiated UI request emitted by the extension on
// its stdout. It uses a string uiId in a namespace disjoint from the integer ids
// of host-initiated requests (execute_tool/command/emit), so the reader can tell
// the two directions apart.
type uiRequestMessage struct {
	Type   string          `json:"type"` // always "ui_request"
	UIID   string          `json:"uiId"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// uiResponseMessage answers a uiRequestMessage back over the extension's stdin.
type uiResponseMessage struct {
	Type   string          `json:"type"` // always "ui_response"
	UIID   string          `json:"uiId"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// handleUIRequest answers one ui_request by invoking the bound host handler and
// writing a ui_response. It runs on its own goroutine (spawned by readLoop) so a
// handler that blocks on interactive input never stalls the stdout reader.
func (r *scriptRuntime) handleUIRequest(req uiRequestMessage) {
	resp := uiResponseMessage{Type: "ui_response", UIID: req.UIID}
	var handler UIRequestHandler
	if r.uiHandler != nil {
		handler = r.uiHandler()
	}
	switch {
	case handler == nil:
		resp.Error = "pi.ui." + req.Method + " requires an interactive host, which is not available"
	default:
		result, err := handler(r.ctx, req.Method, req.Params)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.OK = true
			resp.Result = result
		}
	}
	r.writeUIResponse(resp)
}

func (r *scriptRuntime) writeUIResponse(resp uiResponseMessage) {
	r.writeStdinLine(resp)
}

// writeStdinLine marshals message and writes it as one newline-terminated JSON
// frame, serialized with all other stdin writers via writeMu so concurrent
// frames never interleave on the pipe.
func (r *scriptRuntime) writeStdinLine(message any) {
	data, err := json.Marshal(message)
	if err != nil {
		return
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	_, _ = r.stdin.Write(append(data, '\n'))
}
