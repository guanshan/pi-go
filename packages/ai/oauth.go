package ai

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	aioauth "github.com/guanshan/pi-go/packages/ai/utils/oauth"
)

const (
	cancelMessage                = "Login cancelled"
	deviceFlowTimeoutMessage     = "Device flow timed out"
	deviceFlowSlowDownTimeout    = "Device flow timed out after one or more slow_down responses. This is often caused by clock drift in WSL or VM environments. Please sync or restart the VM clock and try again."
	minimumPollInterval          = time.Second
	defaultPollIntervalSeconds   = 5
	slowDownIntervalIncrement    = 5 * time.Second
	anthropicOAuthClientID       = "9d1c250a-e61b-44d5-88ed-5946d1962f5e"
	anthropicOAuthAuthorizeURL   = "https://claude.ai/oauth/authorize"
	anthropicOAuthTokenURL       = "https://platform.claude.com/v1/oauth/token"
	anthropicOAuthCallbackPort   = 53692
	anthropicOAuthCallbackPath   = "/callback"
	openAICodexOAuthClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAICodexOAuthAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	openAICodexOAuthTokenURL     = "https://auth.openai.com/oauth/token"
	openAICodexOAuthCallbackPort = 1455
	openAICodexOAuthCallbackPath = "/auth/callback"
	openAICodexOAuthScope        = "openid profile email offline_access"
	openAICodexJWTClaimPath      = "https://api.openai.com/auth"
	gitHubCopilotOAuthClientID   = "Iv1.b507a08c87ecfe98"
	gitHubCopilotDefaultDomain   = "github.com"
	gitHubCopilotDefaultBaseURL  = "https://api.individual.githubcopilot.com"
	oauthRefreshExpirySlack      = 5 * time.Minute
)

type oauthFlowError string

func (e oauthFlowError) Error() string { return string(e) }

type OAuthProviderID string

type OAuthCredentials = aioauth.Credentials

type OAuthPrompt struct {
	Message     string `json:"message"`
	Placeholder string `json:"placeholder,omitempty"`
	AllowEmpty  bool   `json:"allowEmpty,omitempty"`
}

type OAuthAuthInfo struct {
	URL          string `json:"url"`
	Instructions string `json:"instructions,omitempty"`
}

type OAuthDeviceCodeInfo struct {
	UserCode         string  `json:"userCode"`
	VerificationURI  string  `json:"verificationUri"`
	IntervalSeconds  float64 `json:"intervalSeconds,omitempty"`
	ExpiresInSeconds float64 `json:"expiresInSeconds,omitempty"`
}

type OAuthDeviceCodePollStatus = aioauth.DeviceCodePollStatus

const (
	OAuthDeviceCodePending  = aioauth.DeviceCodePending
	OAuthDeviceCodeSlowDown = aioauth.DeviceCodeSlowDown
	OAuthDeviceCodeComplete = aioauth.DeviceCodeComplete
	OAuthDeviceCodeFailed   = aioauth.DeviceCodeFailed
)

type OAuthDeviceCodePollResult = aioauth.DeviceCodePollResult

type OAuthDeviceCodePollOptions = aioauth.DeviceCodePollOptions

type OAuthHTTPOptions = aioauth.HTTPOptions

func PollOAuthDeviceCodeFlow(ctx context.Context, options OAuthDeviceCodePollOptions) (string, error) {
	return aioauth.PollDeviceCodeFlow(ctx, options)
}

type OAuthSelectOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type OAuthSelectPrompt struct {
	Message string              `json:"message"`
	Options []OAuthSelectOption `json:"options"`
}

type OAuthLoginCallbacks struct {
	Context           context.Context
	OnAuth            func(OAuthAuthInfo)
	OnDeviceCode      func(OAuthDeviceCodeInfo)
	OnPrompt          func(OAuthPrompt) (string, error)
	OnProgress        func(string)
	OnManualCodeInput func() (string, error)
	OnSelect          func(OAuthSelectPrompt) (string, bool, error)
	// HTTPClient optionally overrides the *http.Client used by the OAuth login
	// flows. When nil, http.DefaultClient is used. This exists so tests can inject
	// a fake transport; production callers leave it unset.
	HTTPClient *http.Client
}

func (c OAuthLoginCallbacks) ctx() context.Context {
	if c.Context != nil {
		return c.Context
	}
	return context.Background()
}

// httpClient returns the injected client, or http.DefaultClient when none was
// provided.
func (c OAuthLoginCallbacks) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

type OAuthProviderInfo struct {
	ID        OAuthProviderID `json:"id"`
	Name      string          `json:"name"`
	Available bool            `json:"available"`
}

type OAuthProvider struct {
	ProviderID       OAuthProviderID
	ProviderName     string
	CallbackServer   bool
	LoginFunc        func(OAuthLoginCallbacks) (OAuthCredentials, error)
	RefreshTokenFunc func(context.Context, OAuthCredentials) (OAuthCredentials, error)
	GetAPIKeyFunc    func(OAuthCredentials) string
	ModifyModelsFunc func([]Model, OAuthCredentials) []Model
}

func (p OAuthProvider) ID() OAuthProviderID { return p.ProviderID }

func (p OAuthProvider) Name() string { return p.ProviderName }

func (p OAuthProvider) UsesCallbackServer() bool { return p.CallbackServer }

func (p OAuthProvider) Login(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	if p.LoginFunc == nil {
		return OAuthCredentials{}, fmt.Errorf("%s OAuth provider does not define a login flow", p.ProviderID)
	}
	return p.LoginFunc(callbacks)
}

func (p OAuthProvider) RefreshToken(ctx context.Context, credentials OAuthCredentials) (OAuthCredentials, error) {
	if p.RefreshTokenFunc == nil {
		return credentials, nil
	}
	return p.RefreshTokenFunc(ctx, credentials)
}

func (p OAuthProvider) GetAPIKey(credentials OAuthCredentials) string {
	if p.GetAPIKeyFunc != nil {
		return p.GetAPIKeyFunc(credentials)
	}
	return credentials.Access
}

func (p OAuthProvider) ModifyModels(models []Model, credentials OAuthCredentials) []Model {
	if p.ModifyModelsFunc != nil {
		return p.ModifyModelsFunc(models, credentials)
	}
	return models
}

var (
	builtinOAuthProviders = []OAuthProvider{
		{
			ProviderID:     "anthropic",
			ProviderName:   "Anthropic (Claude Pro/Max)",
			CallbackServer: true,
			LoginFunc:      LoginAnthropic,
			RefreshTokenFunc: func(ctx context.Context, credentials OAuthCredentials) (OAuthCredentials, error) {
				return RefreshAnthropicToken(ctx, credentials.Refresh)
			},
		},
		{
			ProviderID:   "github-copilot",
			ProviderName: "GitHub Copilot",
			LoginFunc:    LoginGitHubCopilot,
			RefreshTokenFunc: func(ctx context.Context, credentials OAuthCredentials) (OAuthCredentials, error) {
				return RefreshGitHubCopilotToken(ctx, credentials.Refresh, aioauth.StringExtra(credentials, "enterpriseUrl"))
			},
			ModifyModelsFunc: func(models []Model, credentials OAuthCredentials) []Model {
				baseURL := GetGitHubCopilotBaseURL(credentials.Access, aioauth.StringExtra(credentials, "enterpriseUrl"))
				out := append([]Model(nil), models...)
				for i := range out {
					if out[i].Provider == "github-copilot" {
						out[i].BaseURL = baseURL
					}
				}
				return out
			},
		},
		{
			ProviderID:     "openai-codex",
			ProviderName:   "ChatGPT Plus/Pro (Codex Subscription)",
			CallbackServer: true,
			LoginFunc:      LoginOpenAICodex,
			RefreshTokenFunc: func(ctx context.Context, credentials OAuthCredentials) (OAuthCredentials, error) {
				return RefreshOpenAICodexToken(ctx, credentials.Refresh)
			},
		},
	}
	oauthMu       sync.RWMutex
	oauthRegistry = map[OAuthProviderID]OAuthProvider{}
)

func init() {
	ResetOAuthProviders()
}

func GetOAuthProvider(id OAuthProviderID) (OAuthProvider, bool) {
	oauthMu.RLock()
	defer oauthMu.RUnlock()
	provider, ok := oauthRegistry[id]
	return provider, ok
}

func RegisterOAuthProvider(provider OAuthProvider) {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	oauthRegistry[provider.ProviderID] = provider
}

func UnregisterOAuthProvider(id OAuthProviderID) {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	for _, provider := range builtinOAuthProviders {
		if provider.ProviderID == id {
			oauthRegistry[id] = provider
			return
		}
	}
	delete(oauthRegistry, id)
}

func ResetOAuthProviders() {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	oauthRegistry = map[OAuthProviderID]OAuthProvider{}
	for _, provider := range builtinOAuthProviders {
		oauthRegistry[provider.ProviderID] = provider
	}
}

func GetOAuthProviders() []OAuthProvider {
	oauthMu.RLock()
	defer oauthMu.RUnlock()
	out := make([]OAuthProvider, 0, len(oauthRegistry))
	for _, provider := range oauthRegistry {
		out = append(out, provider)
	}
	return out
}

func GetOAuthProviderInfoList() []OAuthProviderInfo {
	providers := GetOAuthProviders()
	out := make([]OAuthProviderInfo, 0, len(providers))
	for _, provider := range providers {
		out = append(out, OAuthProviderInfo{ID: provider.ID(), Name: provider.Name(), Available: true})
	}
	return out
}

func RefreshOAuthToken(ctx context.Context, providerID OAuthProviderID, credentials OAuthCredentials) (OAuthCredentials, error) {
	provider, ok := GetOAuthProvider(providerID)
	if !ok {
		return OAuthCredentials{}, fmt.Errorf("unknown OAuth provider: %s", providerID)
	}
	return provider.RefreshToken(ctx, credentials)
}

type OAuthAPIKeyResult struct {
	NewCredentials OAuthCredentials `json:"newCredentials"`
	APIKey         string           `json:"apiKey"`
}

func GetOAuthAPIKey(ctx context.Context, providerID OAuthProviderID, credentials map[OAuthProviderID]OAuthCredentials) (*OAuthAPIKeyResult, error) {
	provider, ok := GetOAuthProvider(providerID)
	if !ok {
		return nil, fmt.Errorf("unknown OAuth provider: %s", providerID)
	}
	creds, ok := credentials[providerID]
	if !ok {
		return nil, nil
	}
	if creds.Expired(time.Now()) {
		refreshed, err := provider.RefreshToken(ctx, creds)
		if err != nil {
			// Mirror TS: capital F, no wrapped underlying cause.
			return nil, fmt.Errorf("Failed to refresh OAuth token for %s", providerID) //nolint:staticcheck // ST1005: TS-faithful message (oauth/index.ts:150-155, capital F, no wrapped cause)
		}
		creds = refreshed
	}
	return &OAuthAPIKeyResult{NewCredentials: creds, APIKey: provider.GetAPIKey(creds)}, nil
}
