package extensions

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

// newTestScriptRuntime builds a scriptRuntime backed by in-memory pipes instead
// of a real node process, so cancellation behavior can be tested without an
// external dependency. Returns the runtime, a writer to feed it response lines
// (the "extension stdout"), and a reader draining its stdin.
func newTestScriptRuntime(ctx context.Context) (*scriptRuntime, io.WriteCloser, io.ReadCloser) {
	stdoutR, stdoutW := io.Pipe() // extension -> host (responses)
	stdinR, stdinW := io.Pipe()   // host -> extension (requests)

	runtimeCtx, cancel := context.WithCancel(ctx)
	r := &scriptRuntime{
		path:     "test-extension.ts",
		stdin:    stdinW,
		stderr:   &syncBuffer{},
		pending:  make(map[int64]chan scriptResponseMessage),
		readDone: make(chan struct{}),
		ctx:      runtimeCtx,
		cancel:   cancel,
	}
	scanner := bufio.NewScanner(stdoutR)
	go r.readLoop(scanner)
	return r, stdoutW, stdinR
}

// TestRequestCancellationUnblocksPendingRequest verifies that cancelling the
// caller's context unblocks a request that is waiting on an extension that never
// replies.
func TestRequestCancellationUnblocksPendingRequest(t *testing.T) {
	stdinDrain := make(chan struct{})
	reqCtx, cancel := context.WithCancel(context.Background())
	r, stdoutW, stdinR := newTestScriptRuntime(context.Background())
	defer stdoutW.Close()
	defer stdinR.Close()

	// Drain stdin so the request's write does not block on the pipe.
	go func() {
		_, _ = io.Copy(io.Discard, stdinR)
		close(stdinDrain)
	}()

	done := make(chan error, 1)
	go func() {
		_, err := r.request(reqCtx, map[string]any{"type": "emit", "event": "noreply"})
		done <- err
	}()

	// The extension never replies; cancelling must unblock request() promptly.
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected request to fail after cancellation, got nil")
		}
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("request did not unblock after context cancellation")
	}
}

// TestRequestRuntimeContextCancellationUnblocks verifies that cancelling the
// runtime's session context (e.g. on Shutdown) also unblocks a pending request.
func TestRequestRuntimeContextCancellationUnblocks(t *testing.T) {
	r, stdoutW, stdinR := newTestScriptRuntime(context.Background())
	defer stdoutW.Close()
	defer stdinR.Close()
	go func() { _, _ = io.Copy(io.Discard, stdinR) }()

	done := make(chan error, 1)
	go func() {
		_, err := r.request(context.Background(), map[string]any{"type": "emit", "event": "noreply"})
		done <- err
	}()

	r.cancel() // simulate session/runtime teardown
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected request to fail after runtime cancellation, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("request did not unblock after runtime context cancellation")
	}
}

// TestRequestReceivesResponse confirms the happy path still works: a response
// written to stdout is dispatched by id to the waiting request.
func TestRequestReceivesResponse(t *testing.T) {
	r, stdoutW, stdinR := newTestScriptRuntime(context.Background())
	defer stdoutW.Close()
	defer stdinR.Close()

	// Reply to whatever id the request used by echoing back the parsed id.
	go func() {
		dec := json.NewDecoder(stdinR)
		var req map[string]any
		if err := dec.Decode(&req); err != nil {
			return
		}
		id, _ := req["id"].(float64)
		resp, _ := json.Marshal(map[string]any{"id": int64(id), "ok": true, "result": "hi"})
		_, _ = stdoutW.Write(append(resp, '\n'))
	}()

	resp, err := r.request(context.Background(), map[string]any{"type": "emit", "event": "ping"})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if string(resp.Result) != `"hi"` {
		t.Fatalf("unexpected result %q", resp.Result)
	}
}

// TestEventCallbackStopsAfterCancellation verifies the event callback declines
// (does not dispatch / does not mutate the payload) once the runtime context is
// cancelled. It exercises the same gate loadScriptExtension installs.
func TestEventCallbackStopsAfterCancellation(t *testing.T) {
	r, stdoutW, stdinR := newTestScriptRuntime(context.Background())
	defer stdoutW.Close()
	defer stdinR.Close()

	dispatched := make(chan struct{}, 1)
	// Mirror loadScriptExtension's event callback gate.
	callback := func(payload any) {
		if r.ctx.Err() != nil {
			return
		}
		dispatched <- struct{}{}
		_, _ = r.Emit(r.ctx, "evt", payload)
	}

	r.cancel() // cancellation happened before the event fires
	callback(map[string]any{"k": "v"})

	select {
	case <-dispatched:
		t.Fatalf("event callback dispatched after cancellation; expected it to decline")
	case <-time.After(200 * time.Millisecond):
		// Expected: callback returned early without dispatching.
	}
}
