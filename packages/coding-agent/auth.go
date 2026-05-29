package codingagent

import "github.com/guanshan/pi-go/packages/ai"

type AuthCredential struct {
	Provider string `json:"provider"`
	APIKey   string `json:"apiKey,omitempty"`
	Type     string `json:"type,omitempty"`
}

type AuthStatus struct {
	Provider string `json:"provider"`
	HasKey   bool   `json:"hasKey"`
	Source   string `json:"source,omitempty"`
}

func GetAuthStatus(auth *ai.AuthStorage, model ai.Model) AuthStatus {
	key := ""
	source := ""
	if auth != nil {
		key = auth.APIKey(model)
		status := auth.AuthStatus(model.Provider)
		source = status.Source
		if status.Label != "" {
			source += ":" + status.Label
		}
	}
	if source == "" && key != "" {
		source = "env-or-auth-storage"
	}
	return AuthStatus{Provider: model.Provider, HasKey: key != "", Source: source}
}

func SaveAPIKey(auth *ai.AuthStorage, provider, key string) error {
	return auth.SaveAPIKey(provider, key)
}
