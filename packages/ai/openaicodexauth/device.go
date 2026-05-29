package openaicodexauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	aioauth "github.com/guanshan/pi-go/packages/ai/utils/oauth"
)

const (
	DefaultUserCodeURL     = "https://auth.openai.com/api/accounts/deviceauth/usercode"
	DefaultTokenURL        = "https://auth.openai.com/api/accounts/deviceauth/token"
	DefaultVerificationURI = "https://auth.openai.com/codex/device"
	DefaultRedirectURI     = "https://auth.openai.com/deviceauth/callback"
	DefaultTimeoutSeconds  = 15 * 60
)

type Endpoints struct {
	UserCodeURL     string
	TokenURL        string
	VerificationURI string
}

var DefaultEndpoints = Endpoints{
	UserCodeURL:     DefaultUserCodeURL,
	TokenURL:        DefaultTokenURL,
	VerificationURI: DefaultVerificationURI,
}

type DeviceAuth struct {
	DeviceAuthID    string
	UserCode        string
	IntervalSeconds float64
}

func Start(ctx context.Context, client *http.Client, clientID string, endpoints Endpoints) (DeviceAuth, error) {
	endpoints = normalizeEndpoints(endpoints)
	body := strings.NewReader(fmt.Sprintf(`{"client_id":%q}`, clientID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoints.UserCodeURL, body)
	if err != nil {
		return DeviceAuth{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := aioauth.HTTPClient(aioauth.HTTPOptions{Client: client}).Do(req)
	if err != nil {
		return DeviceAuth{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusNotFound {
			return DeviceAuth{}, errors.New("OpenAI Codex device code login is not enabled for this server. Use browser login or verify the server URL")
		}
		return DeviceAuth{}, fmt.Errorf("OpenAI Codex device code request failed with status %d%s", resp.StatusCode, responseBodySuffix(raw))
	}
	var payload struct {
		DeviceAuthID string      `json:"device_auth_id"`
		UserCode     string      `json:"user_code"`
		Interval     json.Number `json:"interval"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return DeviceAuth{}, err
	}
	interval, err := payload.Interval.Float64()
	if err != nil || payload.DeviceAuthID == "" || payload.UserCode == "" || interval < 0 {
		return DeviceAuth{}, fmt.Errorf("invalid OpenAI Codex device code response: %s", string(raw))
	}
	return DeviceAuth{DeviceAuthID: payload.DeviceAuthID, UserCode: payload.UserCode, IntervalSeconds: interval}, nil
}

func Poll(ctx context.Context, client *http.Client, device DeviceAuth, endpoints Endpoints, expiresInSeconds float64) (string, string, error) {
	endpoints = normalizeEndpoints(endpoints)
	encoded, err := aioauth.PollDeviceCodeFlow(ctx, aioauth.DeviceCodePollOptions{
		IntervalSeconds:  device.IntervalSeconds,
		ExpiresInSeconds: expiresInSeconds,
		Poll: func(ctx context.Context) (aioauth.DeviceCodePollResult, error) {
			return pollOnce(ctx, client, endpoints.TokenURL, device)
		},
	})
	if err != nil {
		return "", "", err
	}
	var payload struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
	}
	if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
		return "", "", err
	}
	return payload.AuthorizationCode, payload.CodeVerifier, nil
}

func pollOnce(ctx context.Context, client *http.Client, tokenURL string, device DeviceAuth) (aioauth.DeviceCodePollResult, error) {
	body := strings.NewReader(fmt.Sprintf(`{"device_auth_id":%q,"user_code":%q}`, device.DeviceAuthID, device.UserCode))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, body)
	if err != nil {
		return aioauth.DeviceCodePollResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := aioauth.HTTPClient(aioauth.HTTPOptions{Client: client}).Do(req)
	if err != nil {
		return aioauth.DeviceCodePollResult{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return completePoll(raw), nil
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound || deviceErrorCode(raw) == "deviceauth_authorization_pending" {
		return aioauth.DeviceCodePollResult{Status: aioauth.DeviceCodePending}, nil
	}
	if deviceErrorCode(raw) == "slow_down" {
		return aioauth.DeviceCodePollResult{Status: aioauth.DeviceCodeSlowDown}, nil
	}
	return aioauth.DeviceCodePollResult{
		Status:  aioauth.DeviceCodeFailed,
		Message: fmt.Sprintf("OpenAI Codex device auth failed with status %d%s", resp.StatusCode, responseBodySuffix(raw)),
	}, nil
}

func completePoll(raw []byte) aioauth.DeviceCodePollResult {
	var payload struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.AuthorizationCode == "" || payload.CodeVerifier == "" {
		return aioauth.DeviceCodePollResult{Status: aioauth.DeviceCodeFailed, Message: fmt.Sprintf("Invalid OpenAI Codex device auth token response: %s", string(raw))}
	}
	encoded, _ := json.Marshal(payload)
	return aioauth.DeviceCodePollResult{Status: aioauth.DeviceCodeComplete, AccessToken: string(encoded)}
}

func normalizeEndpoints(endpoints Endpoints) Endpoints {
	if endpoints.UserCodeURL == "" {
		endpoints.UserCodeURL = DefaultUserCodeURL
	}
	if endpoints.TokenURL == "" {
		endpoints.TokenURL = DefaultTokenURL
	}
	if endpoints.VerificationURI == "" {
		endpoints.VerificationURI = DefaultVerificationURI
	}
	return endpoints
}

func deviceErrorCode(raw []byte) string {
	var payload struct {
		Error any `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	switch value := payload.Error.(type) {
	case string:
		return value
	case map[string]any:
		code, _ := value["code"].(string)
		return code
	default:
		return ""
	}
}

func responseBodySuffix(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return ""
	}
	return ": " + text
}
