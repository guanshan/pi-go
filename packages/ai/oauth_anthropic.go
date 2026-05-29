package ai

import (
	"context"
	"errors"
	"net/url"

	aioauth "github.com/guanshan/pi-go/packages/ai/utils/oauth"
)

func LoginAnthropic(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return OAuthCredentials{}, err
	}
	ctx := callbacks.ctx()
	redirectURI := oauthRedirectURI(anthropicOAuthCallbackPort, anthropicOAuthCallbackPath)
	server := startOAuthCallbackServer(ctx, oauthCallbackHost(), anthropicOAuthCallbackPort, anthropicOAuthCallbackPath, pkce.Verifier, "Anthropic authentication completed. You can close this window.")
	defer server.close()

	authURL, err := anthropicAuthorizeURL(pkce, redirectURI)
	if err != nil {
		return OAuthCredentials{}, err
	}
	if callbacks.OnAuth != nil {
		callbacks.OnAuth(OAuthAuthInfo{
			URL:          authURL,
			Instructions: "Complete login in your browser. If the browser is on another machine, paste the final redirect URL here.",
		})
	}

	code, state, err := waitForOAuthCode(callbacks, server, pkce.Verifier, redirectURI)
	if err != nil {
		return OAuthCredentials{}, err
	}
	if code == "" {
		return OAuthCredentials{}, errors.New("missing authorization code")
	}
	if state == "" {
		return OAuthCredentials{}, errors.New("missing OAuth state")
	}
	if callbacks.OnProgress != nil {
		callbacks.OnProgress("Exchanging authorization code for tokens...")
	}
	return exchangeAnthropicAuthorizationCode(ctx, code, state, pkce.Verifier, redirectURI)
}
func anthropicAuthorizeURL(pkce PKCEPair, redirectURI string) (string, error) {
	authURL, err := url.Parse(anthropicOAuthAuthorizeURL)
	if err != nil {
		return "", err
	}
	values := authURL.Query()
	values.Set("code", "true")
	values.Set("client_id", anthropicOAuthClientID)
	values.Set("response_type", "code")
	values.Set("redirect_uri", redirectURI)
	values.Set("scope", "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload")
	values.Set("code_challenge", pkce.Challenge)
	values.Set("code_challenge_method", "S256")
	values.Set("state", pkce.Verifier)
	authURL.RawQuery = values.Encode()
	return authURL.String(), nil
}
func exchangeAnthropicAuthorizationCode(ctx context.Context, code, state, verifier, redirectURI string) (OAuthCredentials, error) {
	return aioauth.ExchangeJSONToken(ctx, anthropicOAuthTokenURL, map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     anthropicOAuthClientID,
		"code":          code,
		"state":         state,
		"redirect_uri":  redirectURI,
		"code_verifier": verifier,
	}, oauthRefreshExpirySlack)
}
func RefreshAnthropicToken(ctx context.Context, refreshToken string, options ...OAuthHTTPOptions) (OAuthCredentials, error) {
	return aioauth.RefreshJSONToken(ctx, refreshToken, anthropicOAuthTokenURL, anthropicOAuthClientID, oauthRefreshExpirySlack, options...)
}
