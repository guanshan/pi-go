package ai

func RegisterBuiltinProviders() {
	registerFauxProvider()
	registerAnthropicProviders()
	registerBedrockProviders()
	registerGoogleProviders()
	registerMistralProviders()
	registerOpenAIProviders()
}
