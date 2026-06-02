package providers

import (
	"fmt"
	"os"
	"strings"
)

const (
	CloudflareWorkersAIBaseURL          = "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/v1"
	CloudflareAIGatewayCompatBaseURL    = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/compat"
	CloudflareAIGatewayOpenAIBaseURL    = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/openai"
	CloudflareAIGatewayAnthropicBaseURL = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/anthropic"
)

func IsCloudflareProvider(provider string) bool {
	return provider == "cloudflare-workers-ai" || provider == "cloudflare-ai-gateway"
}

func HasCloudflareWorkersAICredentials() bool {
	return os.Getenv("CLOUDFLARE_API_KEY") != "" && os.Getenv("CLOUDFLARE_ACCOUNT_ID") != ""
}

func HasCloudflareAIGatewayCredentials() bool {
	return HasCloudflareWorkersAICredentials() && os.Getenv("CLOUDFLARE_GATEWAY_ID") != ""
}

func HasCloudflareRequiredEnv(provider string) bool {
	if os.Getenv("CLOUDFLARE_ACCOUNT_ID") == "" {
		return false
	}
	return provider != "cloudflare-ai-gateway" || os.Getenv("CLOUDFLARE_GATEWAY_ID") != ""
}

func ResolveCloudflareBaseURL(baseURL, provider string) (string, error) {
	return resolveEnvPlaceholders(baseURL, provider)
}

func resolveEnvPlaceholders(value, provider string) (string, error) {
	if !strings.Contains(value, "{") {
		return value, nil
	}
	var out strings.Builder
	for i := 0; i < len(value); {
		if value[i] != '{' {
			out.WriteByte(value[i])
			i++
			continue
		}
		end := strings.IndexByte(value[i+1:], '}')
		if end < 0 {
			out.WriteByte(value[i])
			i++
			continue
		}
		name := value[i+1 : i+1+end]
		if !isEnvPlaceholderName(name) {
			out.WriteString(value[i : i+end+2])
			i += end + 2
			continue
		}
		replacement := os.Getenv(name)
		if replacement == "" {
			return "", fmt.Errorf("%s is required for provider %s but is not set.", name, provider)
		}
		out.WriteString(replacement)
		i += end + 2
	}
	return out.String(), nil
}

func isEnvPlaceholderName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
		case r == '_':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
