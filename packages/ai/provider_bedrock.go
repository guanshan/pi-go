package ai

import (
	"context"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	smithybearer "github.com/aws/smithy-go/auth/bearer"
	smithymiddleware "github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
)

type bedrockAPIProvider struct{}

func registerBedrockProviders() {
	registerBuiltinProvider(bedrockAPIProvider{})
}

func (bedrockAPIProvider) API() string { return "bedrock-converse-stream" }

func (bedrockAPIProvider) complete(ctx context.Context, r *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	return r.bedrockChat(ctx, req)
}

func (p bedrockAPIProvider) Complete(ctx context.Context, r *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	return p.complete(ctx, r, req)
}

func (bedrockAPIProvider) Stream(ctx context.Context, r *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return r.bedrockChatStream(ctx, req)
}

func (p bedrockAPIProvider) StreamSimple(ctx context.Context, r *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return p.Stream(ctx, r, req)
}

func (r *ModelRegistry) bedrockChat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	headers := aiproviders.RequestHeaders(req.Model.Headers, req.Headers)
	input := bedrockConverseInput(req)
	input, err := applyOnPayloadAs[*bedrockruntime.ConverseInput](req, input)
	if err != nil {
		return ChatResponse{}, err
	}
	out, err := r.doBedrockConverse(ctx, req, headers, input)
	if err != nil {
		return ChatResponse{}, err
	}
	msg, calls, err := parseBedrockConverseOutput(out, req.Model)
	if err != nil {
		return ChatResponse{}, err
	}
	return ChatResponse{Message: msg, ToolCalls: calls}, nil
}
func (r *ModelRegistry) bedrockRuntimeClient(ctx context.Context, req ChatRequest, headers map[string]string) (*bedrockruntime.Client, error) {
	model := req.Model
	// Mirror amazon-bedrock.ts: only pin a standard bedrock-runtime endpoint when
	// no region/profile is configured, so AWS_PROFILE's (or AWS_REGION's) region
	// can win over the catalog default base URL. When AWS_PROFILE is set and no
	// explicit region exists, leave region unset so the SDK resolves it from the
	// profile config.
	configuredRegion := aiproviders.BedrockConfiguredRegion()
	hasProfile := aiproviders.BedrockHasConfiguredProfile()
	endpointRegion := aiproviders.BedrockStandardEndpointRegion(model.BaseURL)
	useExplicitEndpoint := aiproviders.ShouldUseExplicitBedrockEndpoint(model.BaseURL, configuredRegion, hasProfile)

	region := ""
	switch {
	case configuredRegion != "":
		region = configuredRegion
	case endpointRegion != "" && useExplicitEndpoint:
		region = endpointRegion
	case !hasProfile:
		region = "us-east-1"
	}

	loadOptions := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithHTTPClient(providerHTTPClient(req)),
	}
	if region != "" {
		loadOptions = append(loadOptions, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, err
	}
	if region == "" {
		// Region was deliberately left unset for the SDK to resolve from the
		// AWS_PROFILE config chain.
		region = cfg.Region
	}
	if region == "" && !hasProfile {
		region = "us-east-1"
	}
	baseEndpoint := aiproviders.BedrockBaseEndpoint(model.BaseURL, region)
	// forcedHeaders are auth headers we set ourselves (the over-http bearer
	// workaround); they bypass the reserved-header filter applied to
	// caller-supplied headers.
	var forcedHeaders map[string]string
	if token := r.bedrockBearerToken(model); token != "" && os.Getenv("AWS_BEDROCK_SKIP_AUTH") != "1" {
		if strings.HasPrefix(strings.ToLower(baseEndpoint), "http://") {
			// The AWS SDK's bearer auth scheme refuses to send Authorization over
			// plaintext http, so set it ourselves and disable SDK signing. This
			// header is intentional and must not be dropped by the reserved-header
			// filter that guards against caller-supplied auth overrides.
			forcedHeaders = map[string]string{"Authorization": "Bearer " + token}
			cfg.Credentials = aws.AnonymousCredentials{}
			cfg.AuthSchemePreference = []string{"noAuth"}
		} else {
			cfg.Credentials = aws.AnonymousCredentials{}
			cfg.BearerAuthTokenProvider = smithybearer.TokenProviderFunc(func(context.Context) (smithybearer.Token, error) {
				return smithybearer.Token{Value: token}, nil
			})
			cfg.AuthSchemePreference = []string{"httpBearerAuth"}
		}
	} else if os.Getenv("AWS_BEDROCK_SKIP_AUTH") == "1" {
		cfg.Credentials = aws.AnonymousCredentials{}
		cfg.AuthSchemePreference = []string{"noAuth"}
	}
	client := bedrockruntime.NewFromConfig(cfg, func(o *bedrockruntime.Options) {
		if region != "" {
			o.Region = region
		}
		if useExplicitEndpoint {
			o.BaseEndpoint = aws.String(baseEndpoint)
		}
		o.HTTPClient = providerHTTPClient(req)
		o.RetryMaxAttempts = 1 + aiproviders.MaxInt(0, req.MaxRetries)
	}, bedrockWithHeaders(headers, forcedHeaders), bedrockWithResponse(req))
	return client, nil
}

func (r *ModelRegistry) doBedrockConverse(ctx context.Context, req ChatRequest, headers map[string]string, input *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
	client, err := r.bedrockRuntimeClient(ctx, req, headers)
	if err != nil {
		return nil, err
	}
	return client.Converse(ctx, input)
}

func (r *ModelRegistry) doBedrockConverseStream(ctx context.Context, req ChatRequest, headers map[string]string, input *bedrockruntime.ConverseStreamInput) (*bedrockruntime.ConverseStreamOutput, error) {
	client, err := r.bedrockRuntimeClient(ctx, req, headers)
	if err != nil {
		return nil, err
	}
	return client.ConverseStream(ctx, input)
}

func (r *ModelRegistry) bedrockBearerToken(model Model) string {
	if r == nil || r.Auth == nil {
		return os.Getenv("AWS_BEARER_TOKEN_BEDROCK")
	}
	return r.Auth.BedrockBearerToken(model)
}

// bedrockIsReservedHeader reports whether key is an auth/SigV4 header that must
// never be overwritten by caller-supplied headers. Mirrors isReservedHeader in
// amazon-bedrock.ts: `host` and `x-amz-*` participate in the SigV4 canonical
// request and `authorization` is owned by SigV4 or the bearer-token path.
// Comparison is case-insensitive.
func bedrockIsReservedHeader(key string) bool {
	lower := strings.ToLower(key)
	return strings.HasPrefix(lower, "x-amz-") || lower == "authorization" || lower == "host"
}

// bedrockWithHeaders applies caller-supplied custom headers to the outgoing
// Bedrock request via a build-step middleware (runs after serialization, before
// SigV4 signing). Reserved auth/SigV4 headers in callerHeaders are silently
// skipped so they cannot break SigV4 or bearer auth, matching TS. forcedHeaders
// are auth headers we deliberately set ourselves (the over-http bearer
// workaround) and are applied unconditionally.
func bedrockWithHeaders(callerHeaders, forcedHeaders map[string]string) func(*bedrockruntime.Options) {
	return func(o *bedrockruntime.Options) {
		if len(callerHeaders) == 0 && len(forcedHeaders) == 0 {
			return
		}
		o.APIOptions = append(o.APIOptions, func(stack *smithymiddleware.Stack) error {
			return stack.Build.Add(smithymiddleware.BuildMiddlewareFunc("piBedrockHeaders", func(ctx context.Context, in smithymiddleware.BuildInput, next smithymiddleware.BuildHandler) (smithymiddleware.BuildOutput, smithymiddleware.Metadata, error) {
				if req, ok := in.Request.(*smithyhttp.Request); ok {
					for key, value := range callerHeaders {
						if bedrockIsReservedHeader(key) {
							continue
						}
						req.Header.Set(key, value)
					}
					for key, value := range forcedHeaders {
						req.Header.Set(key, value)
					}
				}
				return next.HandleBuild(ctx, in)
			}), smithymiddleware.After)
		})
	}
}

func bedrockWithResponse(req ChatRequest) func(*bedrockruntime.Options) {
	return func(o *bedrockruntime.Options) {
		if req.OnResponse == nil {
			return
		}
		o.APIOptions = append(o.APIOptions, func(stack *smithymiddleware.Stack) error {
			return stack.Deserialize.Add(smithymiddleware.DeserializeMiddlewareFunc("piBedrockResponse", func(ctx context.Context, in smithymiddleware.DeserializeInput, next smithymiddleware.DeserializeHandler) (smithymiddleware.DeserializeOutput, smithymiddleware.Metadata, error) {
				out, metadata, err := next.HandleDeserialize(ctx, in)
				if resp, ok := out.RawResponse.(*smithyhttp.Response); ok && resp != nil {
					responseErr := req.OnResponse(ProviderResponse{Status: resp.StatusCode, Headers: aiproviders.HeadersRecord(resp.Header)}, req.Model)
					if responseErr != nil {
						return out, metadata, responseErr
					}
				}
				return out, metadata, err
			}), smithymiddleware.After)
		})
	}
}

func bedrockHasAuth(auth *AuthStorage, model Model) bool {
	if os.Getenv("AWS_BEDROCK_SKIP_AUTH") == "1" {
		return true
	}
	if auth != nil && auth.BedrockBearerToken(model) != "" {
		return true
	}
	_, _, ok := aiproviders.BedrockEnvCredentials()
	return ok
}
