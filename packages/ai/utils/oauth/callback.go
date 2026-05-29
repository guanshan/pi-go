package oauth

import (
	"net/url"
	"strings"
)

func ParseAuthorizationInput(input string) (code string, state string, err error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return "", "", nil
	}
	if parsed, parseErr := url.Parse(value); parseErr == nil && parsed.Scheme != "" {
		return parsed.Query().Get("code"), parsed.Query().Get("state"), nil
	}
	if strings.Contains(value, "#") {
		parts := strings.SplitN(value, "#", 2)
		return parts[0], parts[1], nil
	}
	if strings.Contains(value, "code=") {
		params, parseErr := url.ParseQuery(value)
		if parseErr != nil {
			return "", "", parseErr
		}
		return params.Get("code"), params.Get("state"), nil
	}
	return value, "", nil
}

func SuccessHTML(message string) string {
	return "<!doctype html><html><head><meta charset=\"utf-8\"><title>Authentication complete</title></head><body><h1>Authentication complete</h1><p>" + HTMLEscape(message) + "</p></body></html>"
}

func ErrorHTML(message, details string) string {
	if details != "" {
		details = "<pre>" + HTMLEscape(details) + "</pre>"
	}
	return "<!doctype html><html><head><meta charset=\"utf-8\"><title>Authentication failed</title></head><body><h1>" + HTMLEscape(message) + "</h1>" + details + "</body></html>"
}

func HTMLEscape(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, `"`, "&quot;")
	return value
}
