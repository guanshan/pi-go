package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	aioauth "github.com/guanshan/pi-go/packages/ai/utils/oauth"
)

func LoginGitHubCopilot(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	ctx := callbacks.ctx()
	input := ""
	if callbacks.OnPrompt != nil {
		value, err := callbacks.OnPrompt(OAuthPrompt{
			Message:     "GitHub Enterprise URL/domain (blank for github.com)",
			Placeholder: "company.ghe.com",
			AllowEmpty:  true,
		})
		if err != nil {
			return OAuthCredentials{}, err
		}
		input = value
	}
	if err := ctx.Err(); err != nil {
		return OAuthCredentials{}, oauthFlowError(cancelMessage)
	}
	enterpriseDomain := NormalizeDomain(input)
	if strings.TrimSpace(input) != "" && enterpriseDomain == "" {
		return OAuthCredentials{}, errors.New("invalid GitHub Enterprise URL/domain")
	}
	domain := enterpriseDomain
	if domain == "" {
		domain = gitHubCopilotDefaultDomain
	}

	httpClient := callbacks.httpClient()
	device, err := startGitHubCopilotDeviceFlow(ctx, domain, httpClient)
	if err != nil {
		return OAuthCredentials{}, err
	}
	if callbacks.OnDeviceCode != nil {
		callbacks.OnDeviceCode(OAuthDeviceCodeInfo{
			UserCode:         device.UserCode,
			VerificationURI:  device.VerificationURI,
			IntervalSeconds:  device.IntervalSeconds,
			ExpiresInSeconds: device.ExpiresInSeconds,
		})
	}
	githubAccessToken, err := pollForGitHubCopilotAccessToken(ctx, domain, device, httpClient)
	if err != nil {
		return OAuthCredentials{}, err
	}
	endpoints := gitHubCopilotEndpointFactory(domain)
	credentials, err := RefreshGitHubCopilotToken(ctx, githubAccessToken, enterpriseDomain, OAuthHTTPOptions{Client: httpClient, TokenURL: endpoints.CopilotTokenURL})
	if err != nil {
		return OAuthCredentials{}, err
	}
	if callbacks.OnProgress != nil {
		callbacks.OnProgress("Enabling models...")
	}
	enableAllGitHubCopilotModelsFunc(ctx, credentials.Access, enterpriseDomain, httpClient, callbacks.OnProgress)
	return credentials, nil
}

type gitHubCopilotEndpoints struct {
	DeviceCodeURL   string
	AccessTokenURL  string
	CopilotTokenURL string
}

type gitHubCopilotDeviceCode struct {
	DeviceCode       string
	UserCode         string
	VerificationURI  string
	IntervalSeconds  float64
	ExpiresInSeconds float64
}

var (
	gitHubCopilotEndpointFactory     = defaultGitHubCopilotEndpoints
	gitHubCopilotPollSleep           = aioauth.SleepContext
	enableAllGitHubCopilotModelsFunc = enableAllGitHubCopilotModels
)

func defaultGitHubCopilotEndpoints(domain string) gitHubCopilotEndpoints {
	return gitHubCopilotEndpoints{
		DeviceCodeURL:   "https://" + domain + "/login/device/code",
		AccessTokenURL:  "https://" + domain + "/login/oauth/access_token",
		CopilotTokenURL: "https://api." + domain + "/copilot_internal/v2/token",
	}
}

func startGitHubCopilotDeviceFlow(ctx context.Context, domain string, client *http.Client) (gitHubCopilotDeviceCode, error) {
	endpoints := gitHubCopilotEndpointFactory(domain)
	form := url.Values{
		"client_id": {gitHubCopilotOAuthClientID},
		"scope":     {"read:user"},
	}
	raw, err := aioauth.PostFormJSON(ctx, client, endpoints.DeviceCodeURL, form, map[string]string{"User-Agent": "GitHubCopilotChat/0.35.0"})
	if err != nil {
		return gitHubCopilotDeviceCode{}, err
	}
	var payload struct {
		DeviceCode      string  `json:"device_code"`
		UserCode        string  `json:"user_code"`
		VerificationURI string  `json:"verification_uri"`
		Interval        float64 `json:"interval"`
		ExpiresIn       float64 `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return gitHubCopilotDeviceCode{}, err
	}
	if payload.DeviceCode == "" || payload.UserCode == "" || payload.VerificationURI == "" || payload.ExpiresIn == 0 {
		return gitHubCopilotDeviceCode{}, fmt.Errorf("invalid device code response: %s", string(raw))
	}
	return gitHubCopilotDeviceCode{
		DeviceCode:       payload.DeviceCode,
		UserCode:         payload.UserCode,
		VerificationURI:  payload.VerificationURI,
		IntervalSeconds:  payload.Interval,
		ExpiresInSeconds: payload.ExpiresIn,
	}, nil
}

func pollForGitHubCopilotAccessToken(ctx context.Context, domain string, device gitHubCopilotDeviceCode, client *http.Client) (string, error) {
	endpoints := gitHubCopilotEndpointFactory(domain)
	return PollOAuthDeviceCodeFlow(ctx, OAuthDeviceCodePollOptions{
		IntervalSeconds:  device.IntervalSeconds,
		ExpiresInSeconds: device.ExpiresInSeconds,
		Sleep:            gitHubCopilotPollSleep,
		Poll: func(ctx context.Context) (OAuthDeviceCodePollResult, error) {
			raw, err := aioauth.PostFormJSON(ctx, client, endpoints.AccessTokenURL, url.Values{
				"client_id":   {gitHubCopilotOAuthClientID},
				"device_code": {device.DeviceCode},
				"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			}, map[string]string{"User-Agent": "GitHubCopilotChat/0.35.0"})
			if err != nil {
				return OAuthDeviceCodePollResult{}, err
			}
			var success struct {
				AccessToken string `json:"access_token"`
			}
			if err := json.Unmarshal(raw, &success); err == nil && success.AccessToken != "" {
				return OAuthDeviceCodePollResult{Status: OAuthDeviceCodeComplete, AccessToken: success.AccessToken}, nil
			}
			var failure struct {
				Error            string `json:"error"`
				ErrorDescription string `json:"error_description"`
			}
			if err := json.Unmarshal(raw, &failure); err == nil && failure.Error != "" {
				switch failure.Error {
				case "authorization_pending":
					return OAuthDeviceCodePollResult{Status: OAuthDeviceCodePending}, nil
				case "slow_down":
					return OAuthDeviceCodePollResult{Status: OAuthDeviceCodeSlowDown}, nil
				default:
					message := "Device flow failed: " + failure.Error
					if failure.ErrorDescription != "" {
						message += ": " + failure.ErrorDescription
					}
					return OAuthDeviceCodePollResult{Status: OAuthDeviceCodeFailed, Message: message}, nil
				}
			}
			return OAuthDeviceCodePollResult{Status: OAuthDeviceCodeFailed, Message: "Invalid device token response"}, nil
		},
	})
}

func enableAllGitHubCopilotModels(ctx context.Context, token, enterpriseDomain string, client *http.Client, onProgress func(string)) {
	models := List(AllKnownModels(), "")
	var wg sync.WaitGroup
	for _, model := range models {
		if model.Provider != "github-copilot" {
			continue
		}
		modelID := model.ID
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok := enableGitHubCopilotModel(ctx, token, modelID, enterpriseDomain, client)
			if onProgress != nil {
				if ok {
					onProgress("Enabled " + modelID)
				} else {
					onProgress("Could not enable " + modelID)
				}
			}
		}()
	}
	wg.Wait()
}

func enableGitHubCopilotModel(ctx context.Context, token, modelID, enterpriseDomain string, client *http.Client) bool {
	baseURL := GetGitHubCopilotBaseURL(token, enterpriseDomain)
	body := strings.NewReader(`{"state":"enabled"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/models/"+url.PathEscape(modelID)+"/policy", body)
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("openai-intent", "chat-policy")
	req.Header.Set("x-interaction-type", "chat-policy")
	for key, value := range GitHubCopilotHeaders() {
		req.Header.Set(key, value)
	}
	resp, err := aioauth.HTTPClient(OAuthHTTPOptions{Client: client}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func RefreshGitHubCopilotToken(ctx context.Context, refreshToken, enterpriseDomain string, options ...OAuthHTTPOptions) (OAuthCredentials, error) {
	domain := enterpriseDomain
	if domain == "" {
		domain = gitHubCopilotDefaultDomain
	}
	endpoint := fmt.Sprintf("https://api.%s/copilot_internal/v2/token", domain)
	opt := aioauth.MergeHTTPOptions(options...)
	if opt.TokenURL != "" {
		endpoint = opt.TokenURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return OAuthCredentials{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+refreshToken)
	for key, value := range GitHubCopilotHeaders() {
		req.Header.Set(key, value)
	}
	resp, err := aioauth.HTTPClient(opt).Do(req)
	if err != nil {
		return OAuthCredentials{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return OAuthCredentials{}, fmt.Errorf("GitHub Copilot token refresh failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var payload struct {
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return OAuthCredentials{}, err
	}
	if payload.Token == "" || payload.ExpiresAt == 0 {
		return OAuthCredentials{}, fmt.Errorf("invalid Copilot token response: %s", string(raw))
	}
	creds := OAuthCredentials{
		Refresh: refreshToken,
		Access:  payload.Token,
		Expires: payload.ExpiresAt*1000 - oauthRefreshExpirySlack.Milliseconds(),
		Extra:   map[string]any{},
	}
	if enterpriseDomain != "" {
		creds.Extra["enterpriseUrl"] = enterpriseDomain
	}
	return creds, nil
}

func NormalizeDomain(input string) string {
	return aioauth.NormalizeDomain(input)
}

func GetGitHubCopilotBaseURL(token string, enterpriseDomain string) string {
	return aioauth.GitHubCopilotBaseURL(token, enterpriseDomain)
}

func GitHubCopilotHeaders() map[string]string {
	return aioauth.GitHubCopilotHeaders()
}
