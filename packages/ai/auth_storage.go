package ai

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/guanshan/pi-go/packages/ai/filelock"
	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
)

type AuthStatus struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source,omitempty"`
	Label      string `json:"label,omitempty"`
	Type       string `json:"type,omitempty"`
}

type AuthStorage struct {
	Path       string
	RuntimeKey map[string]string
	Data       map[string]string
	Records    map[string]json.RawMessage
	Types      map[string]string

	// mu serializes credential mutations within a process. Combined with the
	// cross-process lock taken via filelock.WithLock, it prevents concurrent writers
	// (notably parallel OAuth refreshes) from corrupting in-memory maps or
	// clobbering each other's on-disk credentials.
	mu sync.Mutex
}

func NewAuthStorage(agentDir string) *AuthStorage {
	path := filepath.Join(agentDir, "auth.json")
	a := &AuthStorage{Path: path, RuntimeKey: map[string]string{}, Data: map[string]string{}, Records: map[string]json.RawMessage{}, Types: map[string]string{}}
	raw, err := os.ReadFile(path)
	if err == nil {
		_ = a.load(raw)
	}
	return a
}

func (a *AuthStorage) load(raw []byte) error {
	var records map[string]json.RawMessage
	if err := json.Unmarshal(raw, &records); err != nil {
		return err
	}
	for provider, value := range records {
		a.applyRecordValue(provider, value)
	}
	return nil
}

// applyRecordValue decodes a single stored credential record into the in-memory
// Records/Data/Types maps. It is shared by initial load and by persistence so
// the in-memory view always matches what is written to disk.
func (a *AuthStorage) applyRecordValue(provider string, value json.RawMessage) {
	a.ensureMaps()
	a.Records[provider] = append(json.RawMessage(nil), value...)
	var key string
	if err := json.Unmarshal(value, &key); err == nil {
		a.Data[provider] = key
		a.Types[provider] = "api_key"
		return
	}
	var object struct {
		Type   string `json:"type"`
		Key    string `json:"key"`
		Access string `json:"access"`
	}
	if err := json.Unmarshal(value, &object); err != nil {
		return
	}
	if object.Type != "" {
		a.Types[provider] = object.Type
	}
	switch {
	case object.Type == "api_key" && object.Key != "":
		a.Data[provider] = object.Key
	case object.Access != "":
		a.Data[provider] = object.Access
	case object.Key != "":
		a.Data[provider] = object.Key
	}
}

func (a *AuthStorage) ensureMaps() {
	if a.RuntimeKey == nil {
		a.RuntimeKey = map[string]string{}
	}
	if a.Data == nil {
		a.Data = map[string]string{}
	}
	if a.Records == nil {
		a.Records = map[string]json.RawMessage{}
	}
	if a.Types == nil {
		a.Types = map[string]string{}
	}
}

// readDisk returns the current on-disk credential records. A missing file is
// reported as an empty set; a malformed file returns an error so callers abort
// rather than overwrite (and lose) credentials they failed to parse.
func (a *AuthStorage) readDisk() (map[string]json.RawMessage, error) {
	raw, err := os.ReadFile(a.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]json.RawMessage{}, nil
		}
		return nil, err
	}
	if len(raw) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var records map[string]json.RawMessage
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, err
	}
	if records == nil {
		records = map[string]json.RawMessage{}
	}
	return records, nil
}

// persistProviderChange applies a single provider mutation in memory and merges
// it into auth.json under the cross-process lock. Merging (rather than writing
// the whole in-memory map) ensures a concurrent pi process that added or
// refreshed a different provider is not clobbered.
func (a *AuthStorage) persistProviderChange(provider string, record json.RawMessage, deleted bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ensureMaps()
	if deleted {
		delete(a.RuntimeKey, provider)
		delete(a.Data, provider)
		delete(a.Records, provider)
		delete(a.Types, provider)
	} else {
		a.applyRecordValue(provider, record)
	}
	return filelock.WithLock(a.Path, func() error {
		disk, err := a.readDisk()
		if err != nil {
			return err
		}
		if deleted {
			delete(disk, provider)
		} else {
			disk[provider] = record
		}
		return writeJSON(a.Path, disk)
	})
}

func (a *AuthStorage) SetRuntime(provider, key string) {
	if key != "" {
		a.RuntimeKey[provider] = key
	}
}

func (a *AuthStorage) List() []string {
	if a == nil {
		return nil
	}
	seen := map[string]bool{}
	for provider := range a.Records {
		if isStoredEnvKey(provider) {
			continue
		}
		seen[provider] = true
	}
	for provider := range a.Data {
		if !isStoredEnvKey(provider) {
			seen[provider] = true
		}
	}
	out := make([]string, 0, len(seen))
	for provider := range seen {
		out = append(out, provider)
	}
	sort.Strings(out)
	return out
}

func isStoredEnvKey(key string) bool {
	return strings.HasSuffix(key, "_API_KEY")
}

func (a *AuthStorage) Has(provider string) bool {
	if a == nil {
		return false
	}
	if _, ok := a.Records[provider]; ok {
		return true
	}
	return a.Data[provider] != ""
}

func (a *AuthStorage) HasAuth(provider string) bool {
	if a == nil {
		return GetEnvAPIKey(provider) != ""
	}
	if a.Has(provider) {
		return true
	}
	if a.RuntimeKey[provider] != "" {
		return true
	}
	return a.APIKey(Model{Provider: provider}) != ""
}

func (a *AuthStorage) AuthStatus(provider string) AuthStatus {
	if a != nil {
		if a.Has(provider) {
			return AuthStatus{Configured: true, Source: "stored", Type: a.CredentialType(provider)}
		}
		if a.RuntimeKey[provider] != "" {
			return AuthStatus{Source: "runtime", Label: "--api-key"}
		}
		for _, key := range ProviderEnvKeys(provider) {
			if a.Data[key] != "" {
				return AuthStatus{Configured: true, Source: "stored", Label: key, Type: "api_key"}
			}
		}
	}
	for _, env := range ProviderEnvKeys(provider) {
		if os.Getenv(env) != "" {
			return AuthStatus{Source: "environment", Label: env}
		}
	}
	return AuthStatus{}
}

func (a *AuthStorage) CredentialType(provider string) string {
	if a == nil {
		return ""
	}
	if typ := a.Types[provider]; typ != "" {
		return typ
	}
	if a.Data[provider] != "" {
		return "api_key"
	}
	return ""
}

func (a *AuthStorage) APIKey(model Model) string {
	if a != nil {
		if key := a.RuntimeKey[model.Provider]; key != "" {
			return key
		}
		if key := a.Data[model.Provider]; key != "" {
			return key
		}
		if model.EnvKey != "" {
			if key := a.Data[model.EnvKey]; key != "" {
				return key
			}
		}
		for _, env := range ProviderEnvKeys(model.Provider) {
			if key := a.Data[env]; key != "" {
				return key
			}
		}
		if key := a.Data[strings.ToUpper(strings.ReplaceAll(model.Provider, "-", "_"))+"_API_KEY"]; key != "" {
			return key
		}
	}
	if model.EnvKey != "" {
		if key := os.Getenv(model.EnvKey); key != "" {
			return key
		}
	}
	for _, env := range ProviderEnvKeys(model.Provider) {
		if key := os.Getenv(env); key != "" {
			return key
		}
	}
	return ""
}

func (a *AuthStorage) BedrockBearerToken(model Model) string {
	if a != nil {
		if key := a.RuntimeKey[model.Provider]; key != "" {
			return key
		}
		if key := a.Data[model.Provider]; key != "" {
			return key
		}
	}
	if key := os.Getenv("AWS_BEARER_TOKEN_BEDROCK"); key != "" {
		return key
	}
	return ""
}

func (a *AuthStorage) SaveAPIKey(provider, key string) error {
	raw, err := json.Marshal(map[string]string{
		"type": "api_key",
		"key":  key,
	})
	if err != nil {
		return err
	}
	return a.persistProviderChange(provider, raw, false)
}

func (a *AuthStorage) SaveOAuth(provider string, credentials any) error {
	raw, err := marshalOAuthRecord(credentials)
	if err != nil {
		return err
	}
	return a.persistProviderChange(provider, raw, false)
}

// marshalOAuthRecord serializes OAuth credentials with the discriminating
// "type":"oauth" field injected, matching the on-disk credential schema.
func marshalOAuthRecord(credentials any) (json.RawMessage, error) {
	raw, err := json.Marshal(credentials)
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err == nil {
		object["type"] = json.RawMessage(`"oauth"`)
		raw, err = json.Marshal(object)
		if err != nil {
			return nil, err
		}
	}
	return raw, nil
}

func (a *AuthStorage) Delete(provider string) error {
	return a.persistProviderChange(provider, nil, true)
}

// Save merges the full in-memory credential set into auth.json under the
// cross-process lock. It never deletes providers, so a concurrent process that
// stored a new credential is preserved.
func (a *AuthStorage) Save() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return filelock.WithLock(a.Path, func() error {
		disk, err := a.readDisk()
		if err != nil {
			return err
		}
		if a.Records != nil {
			for provider, record := range a.Records {
				disk[provider] = record
			}
			for provider, key := range a.Data {
				if key == "" {
					continue
				}
				if _, ok := disk[provider]; ok {
					continue
				}
				raw, err := json.Marshal(key)
				if err != nil {
					return err
				}
				disk[provider] = raw
			}
		} else {
			for provider, key := range a.Data {
				raw, err := json.Marshal(key)
				if err != nil {
					return err
				}
				disk[provider] = raw
			}
		}
		return writeJSON(a.Path, disk)
	})
}

// RefreshOAuthCredentials refreshes the OAuth token for provider under the
// cross-process lock, mirroring the TypeScript refreshOAuthTokenWithLock. It
// re-reads auth.json after acquiring the lock so that if another process or
// goroutine already refreshed, the still-valid credentials are returned without
// a second refresh (this is the cross-process singleflight guarantee). Only when
// the on-disk token is still expired does it invoke refresh and merge the new
// credentials back without clobbering other providers.
func (a *AuthStorage) RefreshOAuthCredentials(provider string, refresh func(current OAuthCredentials) (OAuthCredentials, error)) (OAuthCredentials, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var (
		result OAuthCredentials
		ok     bool
	)
	err := filelock.WithLock(a.Path, func() error {
		disk, err := a.readDisk()
		if err != nil {
			return err
		}
		raw, exists := disk[provider]
		if !exists {
			return nil
		}
		var current OAuthCredentials
		if err := json.Unmarshal(raw, &current); err != nil {
			return err
		}
		if !current.Expired(time.Now()) {
			// Another process refreshed while we waited for the lock.
			a.applyRecordValue(provider, raw)
			result, ok = current, true
			return nil
		}
		refreshed, err := refresh(current)
		if err != nil {
			return err
		}
		record, err := marshalOAuthRecord(refreshed)
		if err != nil {
			return err
		}
		disk[provider] = record
		a.applyRecordValue(provider, record)
		if err := writeJSON(a.Path, disk); err != nil {
			return err
		}
		result, ok = refreshed, true
		return nil
	})
	if err != nil {
		return OAuthCredentials{}, false, err
	}
	return result, ok, nil
}

func GetEnvAPIKey(provider string) string {
	return aiproviders.GetEnvAPIKey(provider)
}

func ProviderEnvKeys(provider string) []string {
	return aiproviders.ProviderEnvKeys(provider)
}

func writeJSON(path string, value any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "\t")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	// Write atomically: a crash or concurrent writer must never leave a
	// truncated/corrupt auth file that would lose all stored credentials.
	tmp, err := os.CreateTemp(dir, ".auth-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
