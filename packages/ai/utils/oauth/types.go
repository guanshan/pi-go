package oauth

import (
	"bytes"
	"encoding/json"
	"time"
)

type Credentials struct {
	Refresh string         `json:"refresh,omitempty"`
	Access  string         `json:"access,omitempty"`
	Expires int64          `json:"expires,omitempty"`
	Extra   map[string]any `json:"-"`
}

func (c Credentials) MarshalJSON() ([]byte, error) {
	out := map[string]any{}
	for k, v := range c.Extra {
		out[k] = v
	}
	if c.Refresh != "" {
		out["refresh"] = c.Refresh
	}
	if c.Access != "" {
		out["access"] = c.Access
	}
	if c.Expires != 0 {
		out["expires"] = c.Expires
	}
	return json.Marshal(out)
}

func (c *Credentials) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	c.Extra = map[string]any{}
	for k, v := range raw {
		switch k {
		case "refresh":
			if s, ok := v.(string); ok {
				c.Refresh = s
			}
		case "access":
			if s, ok := v.(string); ok {
				c.Access = s
			}
		case "expires":
			switch n := v.(type) {
			case json.Number:
				if i, err := n.Int64(); err == nil {
					c.Expires = i
				} else if f, err := n.Float64(); err == nil {
					c.Expires = int64(f)
				}
			case float64:
				c.Expires = int64(n)
			}
		default:
			c.Extra[k] = v
		}
	}
	return nil
}

func (c Credentials) Expired(now time.Time) bool {
	// Mirror TS getOAuthApiKey / auth-storage: refresh when now >= expires.
	// A zero (or absent) expiry is therefore always treated as expired.
	return now.UnixMilli() >= c.Expires
}
