package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
	openairesponses "github.com/guanshan/pi-go/packages/ai/providers/openairesponses"
	aiutils "github.com/guanshan/pi-go/packages/ai/utils"
)

const (
	openAICodexWebSocketCacheTTL       = 5 * time.Minute
	openAICodexWebSocketConnectTimeout = 15 * time.Second
)

type OpenAICodexWebSocketDebugStats struct {
	Requests                int    `json:"requests"`
	ConnectionsCreated      int    `json:"connectionsCreated"`
	ConnectionsReused       int    `json:"connectionsReused"`
	CachedContextRequests   int    `json:"cachedContextRequests"`
	StoreTrueRequests       int    `json:"storeTrueRequests"`
	FullContextRequests     int    `json:"fullContextRequests"`
	DeltaRequests           int    `json:"deltaRequests"`
	LastInputItems          int    `json:"lastInputItems"`
	LastDeltaInputItems     *int   `json:"lastDeltaInputItems,omitempty"`
	LastPreviousResponseID  string `json:"lastPreviousResponseId,omitempty"`
	WebSocketFailures       int    `json:"websocketFailures"`
	SSEFallbacks            int    `json:"sseFallbacks"`
	WebSocketFallbackActive bool   `json:"websocketFallbackActive,omitempty"`
	LastWebSocketError      string `json:"lastWebSocketError,omitempty"`
}

type openAICodexWebSocketContinuation struct {
	lastRequestBody   map[string]any
	lastResponseID    string
	lastResponseItems []map[string]any
}

type openAICodexWebSocketConnection struct {
	conn         *websocket.Conn
	busy         bool
	idleTimer    *time.Timer
	continuation *openAICodexWebSocketContinuation
}

type openAICodexWebSocketAcquire struct {
	conn    *websocket.Conn
	entry   *openAICodexWebSocketConnection
	reused  bool
	release func(keep bool)
}

var openAICodexWebSocketState = struct {
	sync.Mutex
	sessions     map[string]*openAICodexWebSocketConnection
	stats        map[string]*OpenAICodexWebSocketDebugStats
	sseFallbacks map[string]bool
}{
	sessions:     map[string]*openAICodexWebSocketConnection{},
	stats:        map[string]*OpenAICodexWebSocketDebugStats{},
	sseFallbacks: map[string]bool{},
}

func init() {
	RegisterSessionResourceCleanup(func(sessionID string) error {
		CloseOpenAICodexWebSocketSessions(sessionID)
		return nil
	})
}

func GetOpenAICodexWebSocketDebugStats(sessionID string) (OpenAICodexWebSocketDebugStats, bool) {
	openAICodexWebSocketState.Lock()
	defer openAICodexWebSocketState.Unlock()
	stats := openAICodexWebSocketState.stats[sessionID]
	if stats == nil {
		return OpenAICodexWebSocketDebugStats{}, false
	}
	copy := *stats
	if stats.LastDeltaInputItems != nil {
		value := *stats.LastDeltaInputItems
		copy.LastDeltaInputItems = &value
	}
	return copy, true
}

func ResetOpenAICodexWebSocketDebugStats(sessionIDs ...string) {
	openAICodexWebSocketState.Lock()
	defer openAICodexWebSocketState.Unlock()
	if len(sessionIDs) == 0 {
		openAICodexWebSocketState.stats = map[string]*OpenAICodexWebSocketDebugStats{}
		openAICodexWebSocketState.sseFallbacks = map[string]bool{}
		return
	}
	for _, sessionID := range sessionIDs {
		delete(openAICodexWebSocketState.stats, sessionID)
		delete(openAICodexWebSocketState.sseFallbacks, sessionID)
	}
}

func CloseOpenAICodexWebSocketSessions(sessionIDs ...string) {
	var entries []*openAICodexWebSocketConnection
	openAICodexWebSocketState.Lock()
	if len(sessionIDs) == 0 {
		for _, entry := range openAICodexWebSocketState.sessions {
			entries = append(entries, entry)
		}
		openAICodexWebSocketState.sessions = map[string]*openAICodexWebSocketConnection{}
	} else {
		for _, sessionID := range sessionIDs {
			if entry := openAICodexWebSocketState.sessions[sessionID]; entry != nil {
				entries = append(entries, entry)
			}
			delete(openAICodexWebSocketState.sessions, sessionID)
		}
	}
	openAICodexWebSocketState.Unlock()
	for _, entry := range entries {
		if entry.idleTimer != nil {
			entry.idleTimer.Stop()
		}
		closeOpenAICodexWebSocket(entry.conn, "debug_close")
	}
}

func shouldTryOpenAICodexWebSocket(req ChatRequest) bool {
	if req.Model.API != "openai-codex-responses" {
		return false
	}
	if req.Transport == "sse" {
		return false
	}
	return !openAICodexWebSocketSSEFallbackActive(req.SessionID)
}

func openAICodexTransport(req ChatRequest) string {
	if req.Transport == "" {
		return "auto"
	}
	return req.Transport
}

func openAICodexWebSocketSSEFallbackActive(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	openAICodexWebSocketState.Lock()
	defer openAICodexWebSocketState.Unlock()
	return openAICodexWebSocketState.sseFallbacks[sessionID]
}

func recordOpenAICodexWebSocketSSEFallback(sessionID string) {
	if sessionID == "" {
		return
	}
	openAICodexWebSocketState.Lock()
	defer openAICodexWebSocketState.Unlock()
	stats := openAICodexWebSocketDebugStatsLocked(sessionID)
	stats.SSEFallbacks++
	stats.WebSocketFallbackActive = openAICodexWebSocketState.sseFallbacks[sessionID]
}

func recordOpenAICodexWebSocketFailure(sessionID string, err error) {
	if sessionID == "" {
		return
	}
	openAICodexWebSocketState.Lock()
	defer openAICodexWebSocketState.Unlock()
	openAICodexWebSocketState.sseFallbacks[sessionID] = true
	stats := openAICodexWebSocketDebugStatsLocked(sessionID)
	stats.WebSocketFailures++
	stats.LastWebSocketError = err.Error()
	stats.WebSocketFallbackActive = true
}

func openAICodexWebSocketDebugStatsLocked(sessionID string) *OpenAICodexWebSocketDebugStats {
	stats := openAICodexWebSocketState.stats[sessionID]
	if stats == nil {
		stats = &OpenAICodexWebSocketDebugStats{}
		openAICodexWebSocketState.stats[sessionID] = stats
	}
	return stats
}

func openAICodexWebSocketURL(baseURL string) (string, error) {
	raw := aiproviders.CodexResponsesURL(baseURL)
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported Codex WebSocket URL scheme %q", u.Scheme)
	}
	return u.String(), nil
}

func openAICodexWebSocketHeaders(req ChatRequest, key string) (map[string]string, error) {
	options := openAIResponsesRequestOptions(req)
	headers, err := aiproviders.CodexResponsesHeaders(options, key)
	if err != nil {
		return nil, err
	}
	for name := range headers {
		switch strings.ToLower(name) {
		case "accept", "content-type", "openai-beta":
			delete(headers, name)
		}
	}
	requestID := req.SessionID
	if requestID == "" {
		requestID = uuid.NewString()
	}
	headers["session-id"] = requestID
	headers["x-client-request-id"] = requestID
	return headers, nil
}

func (r *ModelRegistry) runOpenAICodexWebSocket(ctx context.Context, req ChatRequest, key string, body map[string]any, partial AssistantMessage, stream *AssistantMessageEventStream) (AssistantMessage, bool, error) {
	wsURL, err := openAICodexWebSocketURL(req.Model.BaseURL)
	if err != nil {
		return partial, false, err
	}
	headers, err := openAICodexWebSocketHeaders(req, key)
	if err != nil {
		return partial, false, err
	}
	ctx, cancel := openAICodexWebSocketContext(ctx, req)
	defer cancel()

	acquired, err := acquireOpenAICodexWebSocket(ctx, wsURL, headers, req.SessionID)
	if err != nil {
		return partial, false, err
	}
	keepConnection := false
	started := false
	defer func() {
		acquired.release(keepConnection)
	}()

	requestBody := body
	useCachedContext := req.Transport == "websocket-cached" || req.Transport == "auto" || req.Transport == ""
	if useCachedContext && acquired.entry != nil {
		requestBody = buildCachedOpenAICodexWebSocketRequestBody(acquired.entry, body)
	}
	recordOpenAICodexWebSocketRequest(req.SessionID, requestBody, useCachedContext, acquired.reused)

	sendBody := map[string]any{"type": "response.create"}
	for key, value := range requestBody {
		sendBody[key] = value
	}
	rawBody, err := aiproviders.MarshalJSON(sendBody)
	if err != nil {
		return partial, false, err
	}
	if err := acquired.conn.WriteMessage(websocket.TextMessage, rawBody); err != nil {
		return partial, false, err
	}

	state := openairesponses.NewStreamState()
	tracker := newContentStreamTracker()
	for {
		event, terminal, err := readOpenAICodexWebSocketEvent(ctx, acquired.conn, req.IdleTimeoutMs)
		if err != nil {
			openAIResponsesApplyPartial(&partial, openAIResponsesParsedWithRequestDefaults(state.Parsed(), req), req.Model)
			return partial, started, err
		}
		if err := normalizeOpenAICodexWebSocketEvent(event); err != nil {
			return partial, started, err
		}
		if !started {
			started = true
			if stream != nil {
				stream.Push(AssistantMessageEvent{Type: "start", Partial: partial})
			}
		}
		for _, update := range state.Apply(event) {
			openAIResponsesApplyPartial(&partial, openAIResponsesParsedWithRequestDefaults(state.Parsed(), req), req.Model)
			if stream != nil {
				tracker.PushDelta(stream, update.Type, update.ContentIndex, update.Delta, partial)
			}
		}
		if terminal {
			break
		}
	}

	parsed := openAIResponsesParsedWithRequestDefaults(state.Parsed(), req)
	message, _ := openAIResponsesMessage(parsed, req.Model)
	if message.StopReason == "error" {
		if message.ErrorMessage == "" {
			message.ErrorMessage = "Provider returned an error stop reason"
		}
		return message, started, &openAICodexNonTransportError{err: errors.New(message.ErrorMessage)}
	}
	if stream != nil {
		tracker.Finish(stream, message)
	}
	if useCachedContext && acquired.entry != nil && message.ResponseID != "" && ctx.Err() == nil {
		acquired.entry.continuation = &openAICodexWebSocketContinuation{
			lastRequestBody:   cloneMap(body),
			lastResponseID:    message.ResponseID,
			lastResponseItems: openAICodexResponseItemsForContinuation(req, message),
		}
	}
	keepConnection = acquired.entry != nil && ctx.Err() == nil
	return message, started, nil
}

func openAICodexWebSocketContext(ctx context.Context, req ChatRequest) (context.Context, context.CancelFunc) {
	if req.TimeoutMs <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, time.Duration(req.TimeoutMs)*time.Millisecond)
}

func acquireOpenAICodexWebSocket(ctx context.Context, wsURL string, headers map[string]string, sessionID string) (openAICodexWebSocketAcquire, error) {
	if sessionID == "" {
		conn, err := connectOpenAICodexWebSocket(ctx, wsURL, headers)
		if err != nil {
			return openAICodexWebSocketAcquire{}, err
		}
		return openAICodexWebSocketAcquire{
			conn:   conn,
			reused: false,
			release: func(bool) {
				closeOpenAICodexWebSocket(conn, "done")
			},
		}, nil
	}

	openAICodexWebSocketState.Lock()
	cached := openAICodexWebSocketState.sessions[sessionID]
	if cached != nil && cached.idleTimer != nil {
		cached.idleTimer.Stop()
		cached.idleTimer = nil
	}
	if cached != nil && !cached.busy {
		cached.busy = true
		openAICodexWebSocketState.Unlock()
		return openAICodexWebSocketAcquire{
			conn:   cached.conn,
			entry:  cached,
			reused: true,
			release: func(keep bool) {
				releaseOpenAICodexCachedWebSocket(sessionID, cached, keep)
			},
		}, nil
	}
	if cached != nil && cached.busy {
		openAICodexWebSocketState.Unlock()
		conn, err := connectOpenAICodexWebSocket(ctx, wsURL, headers)
		if err != nil {
			return openAICodexWebSocketAcquire{}, err
		}
		return openAICodexWebSocketAcquire{
			conn:   conn,
			reused: false,
			release: func(bool) {
				closeOpenAICodexWebSocket(conn, "done")
			},
		}, nil
	}
	openAICodexWebSocketState.Unlock()

	conn, err := connectOpenAICodexWebSocket(ctx, wsURL, headers)
	if err != nil {
		return openAICodexWebSocketAcquire{}, err
	}
	entry := &openAICodexWebSocketConnection{conn: conn, busy: true}
	openAICodexWebSocketState.Lock()
	openAICodexWebSocketState.sessions[sessionID] = entry
	openAICodexWebSocketState.Unlock()
	return openAICodexWebSocketAcquire{
		conn:   conn,
		entry:  entry,
		reused: false,
		release: func(keep bool) {
			releaseOpenAICodexCachedWebSocket(sessionID, entry, keep)
		},
	}, nil
}

func connectOpenAICodexWebSocket(ctx context.Context, wsURL string, headers map[string]string) (*websocket.Conn, error) {
	dialer := websocket.Dialer{HandshakeTimeout: openAICodexWebSocketConnectTimeout}
	requestHeaders := http.Header{}
	for key, value := range headers {
		requestHeaders.Set(key, value)
	}
	conn, resp, err := dialer.DialContext(ctx, wsURL, requestHeaders)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return conn, nil
}

func releaseOpenAICodexCachedWebSocket(sessionID string, entry *openAICodexWebSocketConnection, keep bool) {
	if !keep {
		openAICodexWebSocketState.Lock()
		if openAICodexWebSocketState.sessions[sessionID] == entry {
			delete(openAICodexWebSocketState.sessions, sessionID)
		}
		openAICodexWebSocketState.Unlock()
		closeOpenAICodexWebSocket(entry.conn, "done")
		return
	}
	openAICodexWebSocketState.Lock()
	entry.busy = false
	if entry.idleTimer != nil {
		entry.idleTimer.Stop()
	}
	entry.idleTimer = time.AfterFunc(openAICodexWebSocketCacheTTL, func() {
		openAICodexWebSocketState.Lock()
		if openAICodexWebSocketState.sessions[sessionID] == entry && !entry.busy {
			delete(openAICodexWebSocketState.sessions, sessionID)
			openAICodexWebSocketState.Unlock()
			closeOpenAICodexWebSocket(entry.conn, "idle_timeout")
			return
		}
		openAICodexWebSocketState.Unlock()
	})
	openAICodexWebSocketState.Unlock()
}

func closeOpenAICodexWebSocket(conn *websocket.Conn, reason string) {
	if conn == nil {
		return
	}
	deadline := time.Now().Add(time.Second)
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, reason), deadline)
	_ = conn.Close()
}

func readOpenAICodexWebSocketEvent(ctx context.Context, conn *websocket.Conn, idleTimeoutMs int) (map[string]any, bool, error) {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	for {
		if idleTimeoutMs > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(time.Duration(idleTimeoutMs) * time.Millisecond))
		} else {
			_ = conn.SetReadDeadline(time.Time{})
		}
		messageType, raw, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil, false, ctx.Err()
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() && idleTimeoutMs > 0 {
				return nil, false, fmt.Errorf("WebSocket idle timeout after %dms", idleTimeoutMs)
			}
			return nil, false, err
		}
		if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, false, &openAICodexNonTransportError{err: fmt.Errorf("Invalid Codex WebSocket JSON: %w", err)}
		}
		eventType, _ := event["type"].(string)
		terminal := eventType == "response.completed" || eventType == "response.done" || eventType == "response.incomplete"
		return event, terminal, nil
	}
}

type openAICodexNonTransportError struct {
	err error
}

func (e *openAICodexNonTransportError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *openAICodexNonTransportError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func isOpenAICodexNonTransportError(err error) bool {
	var target *openAICodexNonTransportError
	return errors.As(err, &target)
}

func normalizeOpenAICodexWebSocketEvent(event map[string]any) error {
	eventType, _ := event["type"].(string)
	switch eventType {
	case "error":
		code, _ := event["code"].(string)
		message, _ := event["message"].(string)
		if message == "" {
			message = code
		}
		if message == "" {
			raw, _ := json.Marshal(event)
			message = string(raw)
		}
		return &openAICodexNonTransportError{err: fmt.Errorf("Codex error: %s", message)}
	case "response.failed":
		message := "Codex response failed"
		code := ""
		if response, ok := event["response"].(map[string]any); ok {
			if errObj, ok := response["error"].(map[string]any); ok {
				if value, _ := errObj["message"].(string); value != "" {
					message = value
				}
				code, _ = errObj["code"].(string)
			}
		}
		if code != "" {
			message = code + ": " + message
		}
		return &openAICodexNonTransportError{err: errors.New(message)}
	case "response.done", "response.incomplete":
		event["type"] = "response.completed"
	}
	return nil
}

func appendOpenAICodexWebSocketFailureDiagnostic(message *AssistantMessage, req ChatRequest, err error, started bool, body map[string]any) {
	if message == nil || err == nil {
		return
	}
	raw, _ := aiproviders.MarshalJSON(body)
	details := map[string]any{
		"configuredTransport": openAICodexTransport(req),
		"eventsEmitted":       started,
		"phase":               "before_message_stream_start",
		"requestBytes":        len(raw),
	}
	if started {
		details["phase"] = "after_message_stream_start"
	} else {
		details["fallbackTransport"] = "sse"
	}
	message.Diagnostics = append(message.Diagnostics, aiutils.CreateAssistantMessageDiagnostic("provider_transport_failure", err, details))
}

func recordOpenAICodexWebSocketRequest(sessionID string, body map[string]any, useCachedContext bool, reused bool) {
	if sessionID == "" {
		return
	}
	openAICodexWebSocketState.Lock()
	defer openAICodexWebSocketState.Unlock()
	stats := openAICodexWebSocketDebugStatsLocked(sessionID)
	stats.Requests++
	if reused {
		stats.ConnectionsReused++
	} else {
		stats.ConnectionsCreated++
	}
	if useCachedContext {
		stats.CachedContextRequests++
	}
	if stored, _ := body["store"].(bool); stored {
		stats.StoreTrueRequests++
	}
	stats.LastInputItems = len(responseInputItems(body["input"]))
	if previous, _ := body["previous_response_id"].(string); previous != "" {
		stats.DeltaRequests++
		value := stats.LastInputItems
		stats.LastDeltaInputItems = &value
		stats.LastPreviousResponseID = previous
	} else {
		stats.FullContextRequests++
		stats.LastDeltaInputItems = nil
		stats.LastPreviousResponseID = ""
	}
}

func buildCachedOpenAICodexWebSocketRequestBody(entry *openAICodexWebSocketConnection, body map[string]any) map[string]any {
	continuation := entry.continuation
	if continuation == nil {
		return body
	}
	delta, ok := cachedOpenAICodexWebSocketInputDelta(body, continuation)
	if !ok || continuation.lastResponseID == "" {
		entry.continuation = nil
		return body
	}
	next := cloneMap(body)
	next["previous_response_id"] = continuation.lastResponseID
	next["input"] = delta
	return next
}

func cachedOpenAICodexWebSocketInputDelta(body map[string]any, continuation *openAICodexWebSocketContinuation) ([]map[string]any, bool) {
	if !requestBodiesMatchExceptInput(body, continuation.lastRequestBody) {
		return nil, false
	}
	current := responseInputItems(body["input"])
	baseline := append(responseInputItems(continuation.lastRequestBody["input"]), continuation.lastResponseItems...)
	if len(current) < len(baseline) {
		return nil, false
	}
	if !responseInputsEqual(current[:len(baseline)], baseline) {
		return nil, false
	}
	return current[len(baseline):], true
}

func requestBodiesMatchExceptInput(a, b map[string]any) bool {
	return jsonStringForCompare(requestBodyWithoutInput(a)) == jsonStringForCompare(requestBodyWithoutInput(b))
}

func requestBodyWithoutInput(body map[string]any) map[string]any {
	out := cloneMap(body)
	delete(out, "input")
	delete(out, "previous_response_id")
	return out
}

func responseInputsEqual(a, b []map[string]any) bool {
	return jsonStringForCompare(a) == jsonStringForCompare(b)
}

func jsonStringForCompare(value any) string {
	raw, _ := aiproviders.MarshalJSON(value)
	return string(raw)
}

func responseInputItems(value any) []map[string]any {
	switch items := value.(type) {
	case []map[string]any:
		return items
	case []any:
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			if mapped, ok := item.(map[string]any); ok {
				out = append(out, mapped)
			}
		}
		return out
	default:
		return nil
	}
}

func openAICodexResponseItemsForContinuation(req ChatRequest, message AssistantMessage) []map[string]any {
	options := openAIResponsesRequestOptions(req)
	options.Messages = openAIResponsesMessages([]Message{message}, req.Model)
	items := aiproviders.ResponsesMessages(options, false)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if item["type"] == "function_call_output" {
			continue
		}
		out = append(out, item)
	}
	return out
}
