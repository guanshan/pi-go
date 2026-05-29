package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func ExchangeJSONToken(ctx context.Context, tokenURL string, body map[string]string, expirySlack time.Duration) (Credentials, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return Credentials{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(raw))
	if err != nil {
		return Credentials{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Credentials{}, err
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Credentials{}, fmt.Errorf("OAuth token exchange failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return ParseTokenResponse(responseBody, expirySlack)
}

func PostFormJSON(ctx context.Context, client *http.Client, endpoint string, form url.Values, extraHeaders map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for key, value := range extraHeaders {
		req.Header.Set(key, value)
	}
	resp, err := HTTPClient(HTTPOptions{Client: client}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

func RefreshFormToken(ctx context.Context, refreshToken, tokenURL, clientID string, expirySlack time.Duration, options ...HTTPOptions) (Credentials, error) {
	opt := MergeHTTPOptions(options...)
	if opt.TokenURL != "" {
		tokenURL = opt.TokenURL
	}
	body := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}
	return RequestFormToken(ctx, tokenURL, body, expirySlack, opt)
}

func RequestFormToken(ctx context.Context, tokenURL string, body url.Values, expirySlack time.Duration, opt HTTPOptions) (Credentials, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(body.Encode()))
	if err != nil {
		return Credentials{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := HTTPClient(opt).Do(req)
	if err != nil {
		return Credentials{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Credentials{}, fmt.Errorf("OAuth token refresh failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return ParseTokenResponse(raw, expirySlack)
}

func RefreshJSONToken(ctx context.Context, refreshToken, tokenURL, clientID string, expirySlack time.Duration, options ...HTTPOptions) (Credentials, error) {
	opt := MergeHTTPOptions(options...)
	if opt.TokenURL != "" {
		tokenURL = opt.TokenURL
	}
	body, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     clientID,
	})
	if err != nil {
		return Credentials{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(body))
	if err != nil {
		return Credentials{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := HTTPClient(opt).Do(req)
	if err != nil {
		return Credentials{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Credentials{}, fmt.Errorf("OAuth token refresh failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return ParseTokenResponse(raw, expirySlack)
}

func ParseTokenResponse(raw []byte, expirySlack time.Duration) (Credentials, error) {
	var payload struct {
		AccessToken  string  `json:"access_token"`
		RefreshToken string  `json:"refresh_token"`
		ExpiresIn    float64 `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Credentials{}, err
	}
	if payload.AccessToken == "" || payload.RefreshToken == "" || payload.ExpiresIn == 0 {
		return Credentials{}, fmt.Errorf("OAuth token response missing fields: %s", string(raw))
	}
	expires := time.Now().Add(DurationFromSeconds(payload.ExpiresIn) - expirySlack).UnixMilli()
	return Credentials{Refresh: payload.RefreshToken, Access: payload.AccessToken, Expires: expires}, nil
}

func StringExtra(credentials Credentials, key string) string {
	if credentials.Extra == nil {
		return ""
	}
	value, _ := credentials.Extra[key].(string)
	return value
}
