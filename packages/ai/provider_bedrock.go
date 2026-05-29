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
	region := aiproviders.BedrockRegion(model.BaseURL)
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
		region = cfg.Region
	}
	if region == "" {
		region = "us-east-1"
	}
	baseEndpoint := aiproviders.BedrockBaseEndpoint(model.BaseURL, region)
	if token := r.bedrockBearerToken(model); token != "" && os.Getenv("AWS_BEDROCK_SKIP_AUTH") != "1" {
		if strings.HasPrefix(strings.ToLower(baseEndpoint), "http://") {
			headers = aiproviders.MergeHeaders(map[string]string{"Authorization": "Bearer " + token}, headers)
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
		o.Region = region
		o.BaseEndpoint = aws.String(baseEndpoint)
		o.HTTPClient = providerHTTPClient(req)
		o.RetryMaxAttempts = 1 + aiproviders.MaxInt(0, req.MaxRetries)
	}, bedrockWithHeaders(headers), bedrockWithResponse(req))
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

func bedrockWithHeaders(headers map[string]string) func(*bedrockruntime.Options) {
	return func(o *bedrockruntime.Options) {
		if len(headers) == 0 {
			return
		}
		o.APIOptions = append(o.APIOptions, func(stack *smithymiddleware.Stack) error {
			return stack.Build.Add(smithymiddleware.BuildMiddlewareFunc("piBedrockHeaders", func(ctx context.Context, in smithymiddleware.BuildInput, next smithymiddleware.BuildHandler) (smithymiddleware.BuildOutput, smithymiddleware.Metadata, error) {
				if req, ok := in.Request.(*smithyhttp.Request); ok {
					for key, value := range headers {
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
