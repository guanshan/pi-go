package ai

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/guanshan/pi-go/packages/ai/openaicodexauth"
	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
	aioauth "github.com/guanshan/pi-go/packages/ai/utils/oauth"
)

const (
	openAICodexBrowserLoginMethod    = "browser"
	openAICodexDeviceCodeLoginMethod = "device_code"
)

var openAICodexDeviceEndpoints = openaicodexauth.DefaultEndpoints

func LoginOpenAICodex(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	if callbacks.OnSelect != nil {
		method, ok, err := callbacks.OnSelect(OAuthSelectPrompt{
			Message: "Select OpenAI Codex login method:",
			Options: []OAuthSelectOption{
				{ID: openAICodexBrowserLoginMethod, Label: "Browser login (default)"},
				{ID: openAICodexDeviceCodeLoginMethod, Label: "Device code login (headless)"},
			},
		})
		if err != nil {
			return OAuthCredentials{}, err
		}
		if !ok {
			return OAuthCredentials{}, errors.New("Login cancelled")
		}
		switch method {
		case openAICodexDeviceCodeLoginMethod:
			return LoginOpenAICodexDeviceCode(callbacks)
		case "", openAICodexBrowserLoginMethod:
		default:
			return OAuthCredentials{}, fmt.Errorf("unknown OpenAI Codex login method: %s", method)
		}
	}
	return loginOpenAICodexBrowser(callbacks)
}

func loginOpenAICodexBrowser(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return OAuthCredentials{}, err
	}
	state, err := randomHex(16)
	if err != nil {
		return OAuthCredentials{}, err
	}
	ctx := callbacks.ctx()
	redirectURI := oauthRedirectURI(openAICodexOAuthCallbackPort, openAICodexOAuthCallbackPath)
	server := startOAuthCallbackServer(ctx, oauthCallbackHost(), openAICodexOAuthCallbackPort, openAICodexOAuthCallbackPath, state, "OpenAI authentication completed. You can close this window.")
	defer server.close()

	authURL, err := openAICodexAuthorizeURL(pkce, state, redirectURI)
	if err != nil {
		return OAuthCredentials{}, err
	}
	if callbacks.OnAuth != nil {
		callbacks.OnAuth(OAuthAuthInfo{URL: authURL, Instructions: "A browser window should open. Complete login to finish."})
	}

	code, _, err := waitForOAuthCode(callbacks, server, state, redirectURI)
	if err != nil {
		return OAuthCredentials{}, err
	}
	if code == "" {
		return OAuthCredentials{}, errors.New("missing authorization code")
	}
	credentials, err := exchangeOpenAICodexAuthorizationCode(ctx, code, pkce.Verifier, redirectURI, OAuthHTTPOptions{Client: callbacks.httpClient()})
	if err != nil {
		return OAuthCredentials{}, err
	}
	if _, err := aiproviders.ExtractCodexAccountID(credentials.Access); err != nil {
		return OAuthCredentials{}, err
	}
	return credentials, nil
}

func LoginOpenAICodexDeviceCode(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	ctx := callbacks.ctx()
	httpClient := callbacks.httpClient()
	device, err := openaicodexauth.Start(ctx, httpClient, openAICodexOAuthClientID, openAICodexDeviceEndpoints)
	if err != nil {
		return OAuthCredentials{}, err
	}
	if callbacks.OnDeviceCode != nil {
		callbacks.OnDeviceCode(OAuthDeviceCodeInfo{
			UserCode:         device.UserCode,
			VerificationURI:  openAICodexDeviceEndpoints.VerificationURI,
			IntervalSeconds:  device.IntervalSeconds,
			ExpiresInSeconds: openaicodexauth.DefaultTimeoutSeconds,
		})
	}
	code, verifier, err := openaicodexauth.Poll(ctx, httpClient, device, openAICodexDeviceEndpoints, openaicodexauth.DefaultTimeoutSeconds)
	if err != nil {
		return OAuthCredentials{}, err
	}
	return exchangeOpenAICodexAuthorizationCode(ctx, code, verifier, openaicodexauth.DefaultRedirectURI, OAuthHTTPOptions{Client: httpClient})
}
func openAICodexAuthorizeURL(pkce PKCEPair, state, redirectURI string) (string, error) {
	authURL, err := url.Parse(openAICodexOAuthAuthorizeURL)
	if err != nil {
		return "", err
	}
	values := authURL.Query()
	values.Set("response_type", "code")
	values.Set("client_id", openAICodexOAuthClientID)
	values.Set("redirect_uri", redirectURI)
	values.Set("scope", openAICodexOAuthScope)
	values.Set("code_challenge", pkce.Challenge)
	values.Set("code_challenge_method", "S256")
	values.Set("state", state)
	values.Set("id_token_add_organizations", "true")
	values.Set("codex_cli_simplified_flow", "true")
	values.Set("originator", "pi")
	authURL.RawQuery = values.Encode()
	return authURL.String(), nil
}
func exchangeOpenAICodexAuthorizationCode(ctx context.Context, code, verifier, redirectURI string, opts ...OAuthHTTPOptions) (OAuthCredentials, error) {
	body := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {openAICodexOAuthClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	}
	credentials, err := aioauth.RequestFormToken(ctx, openAICodexOAuthTokenURL, body, 0, aioauth.MergeHTTPOptions(opts...))
	if err != nil {
		return OAuthCredentials{}, err
	}
	accountID, err := aiproviders.ExtractCodexAccountID(credentials.Access)
	if err != nil {
		return OAuthCredentials{}, err
	}
	credentials.Extra = map[string]any{"accountId": accountID}
	return credentials, nil
}
func RefreshOpenAICodexToken(ctx context.Context, refreshToken string, options ...OAuthHTTPOptions) (OAuthCredentials, error) {
	credentials, err := aioauth.RefreshFormToken(ctx, refreshToken, openAICodexOAuthTokenURL, openAICodexOAuthClientID, 0, options...)
	if err != nil {
		return OAuthCredentials{}, err
	}
	accountID, err := aiproviders.ExtractCodexAccountID(credentials.Access)
	if err != nil {
		return OAuthCredentials{}, err
	}
	credentials.Extra = map[string]any{"accountId": accountID}
	return credentials, nil
}
