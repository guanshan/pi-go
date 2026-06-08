package extensions

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
)

// readLoop is the single owner of the stdout scanner. It dispatches each
// response to the per-request channel keyed by id, and on EOF/scan error records
// the terminal error and wakes every pending request (and future ones) so none
// block forever on a dead process.
func (r *scriptRuntime) readLoop(scanner *bufio.Scanner) {
	for scanner.Scan() {
		line := scanner.Bytes()
		// A server-initiated UI request from the extension (ctx.ui.*) carries
		// type:"ui_request" and a string uiId, distinct from the integer-id
		// responses to host-initiated requests. Dispatch it on its own goroutine so
		// a handler that blocks on interactive input never stalls this reader.
		var probe struct {
			Type     string `json:"type"`
			UIID     string `json:"uiId"`
			ActionID string `json:"actionId"`
		}
		// Classify the line with a single decode; every host-bound message type
		// below reuses this probe instead of re-parsing the same bytes.
		probeErr := json.Unmarshal(line, &probe)
		if probeErr == nil && (probe.Type == "shortcut_registered" || probe.Type == "shortcut_unregistered") {
			var req scriptShortcutUpdateMessage
			if err := json.Unmarshal(line, &req); err == nil {
				switch req.Type {
				case "shortcut_registered":
					if r.shortcutRegister != nil {
						r.shortcutRegister(req.Shortcut)
					}
				case "shortcut_unregistered":
					if r.shortcutUnregister != nil {
						r.shortcutUnregister(req.Key)
					}
				}
			}
			continue
		}
		if probeErr == nil && (probe.Type == "provider_registered" || probe.Type == "provider_unregistered") {
			var req scriptProviderUpdateMessage
			if err := json.Unmarshal(line, &req); err == nil {
				switch req.Type {
				case "provider_registered":
					if r.providerRegister != nil {
						r.providerRegister(req.Provider)
					}
				case "provider_unregistered":
					if r.providerUnregister != nil {
						r.providerUnregister(req.ProviderName, req.API)
					}
				}
			}
			continue
		}
		if probeErr == nil && (probe.Type == "message_renderer_registered" || probe.Type == "message_renderer_unregistered") {
			var req scriptMessageRendererUpdateMessage
			if err := json.Unmarshal(line, &req); err == nil {
				switch req.Type {
				case "message_renderer_registered":
					if r.messageRendererRegister != nil {
						r.messageRendererRegister(req.Renderer)
					}
				case "message_renderer_unregistered":
					if r.messageRendererUnregister != nil {
						r.messageRendererUnregister(req.CustomType)
					}
				}
			}
			continue
		}
		if probeErr == nil && probe.Type == "provider_chunk" {
			// Token-level streaming event. Route to the ProviderStream consumer for
			// this call id with a NON-BLOCKING send: if the consumer is gone or its
			// buffer is full, drop the chunk (the final integer-id reply carries the
			// authoritative full message) rather than stall this single reader.
			var chunk scriptProviderChunk
			if err := json.Unmarshal(line, &chunk); err == nil {
				r.pendingMu.Lock()
				ch := r.providerChunks[chunk.CallID]
				r.pendingMu.Unlock()
				if ch != nil {
					select {
					case ch <- chunk.Event:
					default:
					}
				}
			}
			continue
		}
		if probeErr == nil && probe.Type == "ui_request" {
			var req uiRequestMessage
			if err := json.Unmarshal(line, &req); err != nil {
				// Improbable (the probe parsed): reject so the extension's awaiting
				// ctx.ui promise doesn't hang forever on a dropped request.
				go r.writeUIResponse(uiResponseMessage{Type: "ui_response", UIID: probe.UIID, Error: "malformed ui_request"})
			} else {
				go r.handleUIRequest(req)
			}
			continue
		}
		if probeErr == nil && probe.Type == "context_action_request" {
			var req contextActionRequestMessage
			if err := json.Unmarshal(line, &req); err != nil {
				go r.writeContextActionResponse(contextActionResponseMessage{Type: "context_action_response", ActionID: probe.ActionID, Error: "malformed context_action_request"})
			} else {
				go r.handleContextActionRequest(req)
			}
			continue
		}
		var response scriptResponseMessage
		if err := json.Unmarshal(line, &response); err != nil {
			// A malformed line is not addressable to a request id; skip it rather
			// than tear down the runtime (mirrors ignoring non-response chatter).
			continue
		}
		r.pendingMu.Lock()
		ch, ok := r.pending[response.ID]
		if ok {
			delete(r.pending, response.ID)
		}
		r.pendingMu.Unlock()
		if ok {
			ch <- response
		}
	}
	loopErr := scanner.Err()
	if loopErr == nil {
		loopErr = io.EOF
	}
	r.pendingMu.Lock()
	r.readErr = loopErr
	close(r.readDone)
	r.pendingMu.Unlock()
}

func (r *scriptRuntime) request(ctx context.Context, payload map[string]any) (scriptResponseMessage, error) {
	if r == nil {
		return scriptResponseMessage{}, fmt.Errorf("script extension runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// Honor both the caller's context and the runtime's session context, so
	// cancelling either unblocks the request.
	if err := ctx.Err(); err != nil {
		return scriptResponseMessage{}, err
	}
	if r.ctx != nil {
		if err := r.ctx.Err(); err != nil {
			return scriptResponseMessage{}, err
		}
	}

	id := atomic.AddInt64(&r.nextID, 1)
	payload["id"] = id
	if r.contextProvider != nil {
		payload["context"] = r.contextProvider()
	}
	line, err := json.Marshal(payload)
	if err != nil {
		return scriptResponseMessage{}, err
	}

	// Register the response channel BEFORE writing, so a fast reply cannot race
	// ahead of the registration. Buffered so the reader never blocks delivering.
	respCh := make(chan scriptResponseMessage, 1)
	r.pendingMu.Lock()
	// If the reader already terminated, fail fast instead of blocking forever.
	if r.readErr != nil {
		readErr := r.readErr
		r.pendingMu.Unlock()
		return scriptResponseMessage{}, fmt.Errorf("%s: extension runtime stopped: %w%s", r.path, readErr, scriptStderrSuffix(r.stderr))
	}
	r.pending[id] = respCh
	r.pendingMu.Unlock()

	cleanup := func() {
		r.pendingMu.Lock()
		delete(r.pending, id)
		r.pendingMu.Unlock()
	}

	// Serialize only the stdin write; never hold the lock across the wait below.
	r.writeMu.Lock()
	_, writeErr := r.stdin.Write(append(line, '\n'))
	r.writeMu.Unlock()
	if writeErr != nil {
		cleanup()
		return scriptResponseMessage{}, fmt.Errorf("%s: failed to write extension request: %w%s", r.path, writeErr, scriptStderrSuffix(r.stderr))
	}

	rctxDone := func() <-chan struct{} {
		if r.ctx == nil {
			return nil
		}
		return r.ctx.Done()
	}()

	select {
	case response := <-respCh:
		if response.ID != id {
			return scriptResponseMessage{}, fmt.Errorf("%s: extension response id mismatch: got %d want %d", r.path, response.ID, id)
		}
		if !response.OK {
			return scriptResponseMessage{}, fmt.Errorf("%s: %s%s", r.path, firstNonEmpty(response.Error, "extension request failed"), scriptStderrSuffix(r.stderr))
		}
		return response, nil
	case <-ctx.Done():
		r.sendCancelRequest(id)
		cleanup()
		return scriptResponseMessage{}, ctx.Err()
	case <-rctxDone:
		cleanup()
		return scriptResponseMessage{}, r.ctx.Err()
	case <-r.readDone:
		cleanup()
		r.pendingMu.Lock()
		readErr := r.readErr
		r.pendingMu.Unlock()
		if readErr == nil {
			readErr = io.EOF
		}
		return scriptResponseMessage{}, fmt.Errorf("%s: extension runtime stopped: %w%s", r.path, readErr, scriptStderrSuffix(r.stderr))
	}
}

// ProviderStream is request()'s streaming sibling for provider_call: it registers
// a provider_chunk channel alongside the response channel, calls emit for each
// token-level chunk as it arrives, and returns the final integer-id reply (the
// authoritative message/metadata). Chunks for a call always precede that reply on
// the single ordered stdout stream, so any still-buffered chunks are drained
// before the terminal reply is returned. Cancellation mirrors request().
func (r *scriptRuntime) ProviderStream(ctx context.Context, payload map[string]any, emit func(scriptProviderChunkEvent)) (scriptResponseMessage, error) {
	if r == nil {
		return scriptResponseMessage{}, fmt.Errorf("script extension runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return scriptResponseMessage{}, err
	}
	if r.ctx != nil {
		if err := r.ctx.Err(); err != nil {
			return scriptResponseMessage{}, err
		}
	}

	id := atomic.AddInt64(&r.nextID, 1)
	payload["id"] = id
	if r.contextProvider != nil {
		payload["context"] = r.contextProvider()
	}
	line, err := json.Marshal(payload)
	if err != nil {
		return scriptResponseMessage{}, err
	}

	respCh := make(chan scriptResponseMessage, 1)
	chunkCh := make(chan scriptProviderChunkEvent, 64)
	r.pendingMu.Lock()
	if r.readErr != nil {
		readErr := r.readErr
		r.pendingMu.Unlock()
		return scriptResponseMessage{}, fmt.Errorf("%s: extension runtime stopped: %w%s", r.path, readErr, scriptStderrSuffix(r.stderr))
	}
	r.pending[id] = respCh
	r.providerChunks[id] = chunkCh
	r.pendingMu.Unlock()

	cleanup := func() {
		r.pendingMu.Lock()
		delete(r.pending, id)
		delete(r.providerChunks, id)
		r.pendingMu.Unlock()
	}

	r.writeMu.Lock()
	_, writeErr := r.stdin.Write(append(line, '\n'))
	r.writeMu.Unlock()
	if writeErr != nil {
		cleanup()
		return scriptResponseMessage{}, fmt.Errorf("%s: failed to write extension request: %w%s", r.path, writeErr, scriptStderrSuffix(r.stderr))
	}

	rctxDone := func() <-chan struct{} {
		if r.ctx == nil {
			return nil
		}
		return r.ctx.Done()
	}()

	deliver := func(event scriptProviderChunkEvent) {
		if emit != nil {
			emit(event)
		}
	}

	for {
		select {
		case event := <-chunkCh:
			deliver(event)
		case response := <-respCh:
			// Drain any chunks the reader buffered before this reply so incremental
			// events are never lost when select races the reply ahead of them.
			for draining := true; draining; {
				select {
				case event := <-chunkCh:
					deliver(event)
				default:
					draining = false
				}
			}
			cleanup()
			if response.ID != id {
				return scriptResponseMessage{}, fmt.Errorf("%s: extension response id mismatch: got %d want %d", r.path, response.ID, id)
			}
			if !response.OK {
				return scriptResponseMessage{}, fmt.Errorf("%s: %s%s", r.path, firstNonEmpty(response.Error, "extension request failed"), scriptStderrSuffix(r.stderr))
			}
			return response, nil
		case <-ctx.Done():
			r.sendCancelRequest(id)
			cleanup()
			return scriptResponseMessage{}, ctx.Err()
		case <-rctxDone:
			cleanup()
			return scriptResponseMessage{}, r.ctx.Err()
		case <-r.readDone:
			cleanup()
			r.pendingMu.Lock()
			readErr := r.readErr
			r.pendingMu.Unlock()
			if readErr == nil {
				readErr = io.EOF
			}
			return scriptResponseMessage{}, fmt.Errorf("%s: extension runtime stopped: %w%s", r.path, readErr, scriptStderrSuffix(r.stderr))
		}
	}
}

func (r *scriptRuntime) sendCancelRequest(id int64) {
	if r == nil || r.stdin == nil || id <= 0 {
		return
	}
	line, err := json.Marshal(map[string]any{"type": "cancel_request", "id": id})
	if err != nil {
		return
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	_, _ = r.stdin.Write(append(line, '\n'))
}

// syncBuffer is a goroutine-safe bytes.Buffer wrapper. os/exec's stderr copy
// goroutine writes to cmd.Stderr while the process runs, while request() and
// scriptStderrSuffix read it concurrently to attach stderr context to errors;
// the mutex prevents a data race between those reads and the copy goroutine's
// writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func scriptStderrSuffix(stderr *syncBuffer) string {
	if stderr == nil || stderr.Len() == 0 {
		return ""
	}
	text := strings.TrimSpace(stderr.String())
	if text == "" {
		return ""
	}
	if len(text) > 4096 {
		text = text[len(text)-4096:]
	}
	return ": " + text
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
