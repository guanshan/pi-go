package ai

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"

	aioauth "github.com/guanshan/pi-go/packages/ai/utils/oauth"
)

type PKCEPair = aioauth.PKCEPair

func GeneratePKCE() (PKCEPair, error) {
	return aioauth.GeneratePKCE()
}

type oauthCallbackResult struct {
	Code  string
	State string
}

type oauthCallbackServer struct {
	close      func()
	cancelWait func()
	wait       func() (*oauthCallbackResult, error)
}

func startOAuthCallbackServer(ctx context.Context, host string, port int, path, expectedState, successMessage string) oauthCallbackServer {
	resultCh := make(chan *oauthCallbackResult, 1)
	var settleOnce sync.Once
	settle := func(result *oauthCallbackResult) {
		settleOnce.Do(func() {
			resultCh <- result
		})
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		settle(nil)
		return oauthCallbackServer{
			close:      func() {},
			cancelWait: func() { settle(nil) },
			wait: func() (*oauthCallbackResult, error) {
				select {
				case result := <-resultCh:
					return result, nil
				case <-ctx.Done():
					return nil, oauthFlowError(cancelMessage)
				}
			},
		}
	}
	mux := http.NewServeMux()
	server := &http.Server{Handler: mux}
	done := make(chan struct{})
	var closeOnce sync.Once
	closeServer := func() {
		closeOnce.Do(func() {
			close(done)
			settle(nil)
			_ = server.Close()
		})
	}
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if oauthError := query.Get("error"); oauthError != "" {
			writeOAuthHTML(w, http.StatusBadRequest, oauthErrorHTML("Authentication did not complete.", "Error: "+oauthError))
			return
		}
		code := query.Get("code")
		state := query.Get("state")
		if code == "" {
			writeOAuthHTML(w, http.StatusBadRequest, oauthErrorHTML("Missing authorization code.", ""))
			return
		}
		if expectedState != "" && state != expectedState {
			writeOAuthHTML(w, http.StatusBadRequest, oauthErrorHTML("State mismatch.", ""))
			return
		}
		writeOAuthHTML(w, http.StatusOK, oauthSuccessHTML(successMessage))
		settle(&oauthCallbackResult{Code: code, State: state})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeOAuthHTML(w, http.StatusNotFound, oauthErrorHTML("Callback route not found.", ""))
	})
	go func() {
		_ = server.Serve(listener)
	}()
	go func() {
		select {
		case <-ctx.Done():
			closeServer()
		case <-done:
		}
	}()
	return oauthCallbackServer{
		close:      closeServer,
		cancelWait: func() { settle(nil) },
		wait: func() (*oauthCallbackResult, error) {
			select {
			case result := <-resultCh:
				return result, nil
			case <-ctx.Done():
				return nil, oauthFlowError(cancelMessage)
			}
		},
	}
}

func waitForOAuthCode(callbacks OAuthLoginCallbacks, server oauthCallbackServer, expectedState, promptPlaceholder string) (string, string, error) {
	var manualCh chan oauthManualResult
	if callbacks.OnManualCodeInput != nil {
		manualCh = make(chan oauthManualResult, 1)
		go func() {
			input, err := callbacks.OnManualCodeInput()
			server.cancelWait()
			manualCh <- oauthManualResult{Input: input, Err: err}
		}()
	}

	result, err := server.wait()
	if err != nil {
		return "", "", err
	}
	if result != nil && result.Code != "" {
		return result.Code, result.State, nil
	}
	if manualCh != nil {
		manual := <-manualCh
		if manual.Err != nil {
			return "", "", manual.Err
		}
		code, state, err := parseAuthorizationInput(manual.Input)
		if err != nil {
			return "", "", err
		}
		if state != "" && expectedState != "" && state != expectedState {
			return "", "", oauthFlowError("OAuth state mismatch")
		}
		if code != "" {
			return code, firstString(state, expectedState), nil
		}
	}
	if callbacks.OnPrompt == nil {
		return "", "", oauthFlowError("OAuth login requires a prompt callback or callback server")
	}
	input, err := callbacks.OnPrompt(OAuthPrompt{
		Message:     "Paste the authorization code or full redirect URL:",
		Placeholder: promptPlaceholder,
	})
	if err != nil {
		return "", "", err
	}
	code, state, err := parseAuthorizationInput(input)
	if err != nil {
		return "", "", err
	}
	if state != "" && expectedState != "" && state != expectedState {
		return "", "", oauthFlowError("OAuth state mismatch")
	}
	return code, firstString(state, expectedState), nil
}

type oauthManualResult struct {
	Input string
	Err   error
}

func parseAuthorizationInput(input string) (code string, state string, err error) {
	return aioauth.ParseAuthorizationInput(input)
}

func randomHex(bytesLen int) (string, error) {
	return aioauth.RandomHex(bytesLen)
}

func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func oauthCallbackHost() string {
	if host := os.Getenv("PI_OAUTH_CALLBACK_HOST"); host != "" {
		return host
	}
	return "127.0.0.1"
}

func oauthRedirectURI(port int, path string) string {
	return "http://localhost:" + strconv.Itoa(port) + path
}

func writeOAuthHTML(w http.ResponseWriter, status int, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, html)
}

func oauthSuccessHTML(message string) string {
	return aioauth.SuccessHTML(message)
}

func oauthErrorHTML(message, details string) string {
	return aioauth.ErrorHTML(message, details)
}
