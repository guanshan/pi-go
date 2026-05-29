package oauth

import "net/http"

type HTTPOptions struct {
	Client   *http.Client
	TokenURL string
}

func MergeHTTPOptions(options ...HTTPOptions) HTTPOptions {
	var out HTTPOptions
	for _, option := range options {
		if option.Client != nil {
			out.Client = option.Client
		}
		if option.TokenURL != "" {
			out.TokenURL = option.TokenURL
		}
	}
	return out
}

func HTTPClient(option HTTPOptions) *http.Client {
	if option.Client != nil {
		return option.Client
	}
	return http.DefaultClient
}
