package harness

import "github.com/guanshan/pi-go/packages/ai"

func streamOptionsFromAI(opts ai.StreamOptions) StreamOptions {
	return StreamOptions{
		Transport:       opts.Transport,
		TimeoutMs:       opts.TimeoutMs,
		MaxRetries:      opts.MaxRetries,
		MaxRetryDelayMs: opts.MaxRetryDelayMs,
		Headers:         mergeStringMaps(opts.Headers),
		Metadata:        mergeAnyMaps(opts.Metadata),
		CacheRetention:  opts.CacheRetention,
	}
}

func applyStreamOptionsToAI(opts *ai.StreamOptions, source StreamOptions) {
	opts.Transport = source.Transport
	opts.TimeoutMs = source.TimeoutMs
	opts.MaxRetries = source.MaxRetries
	opts.MaxRetryDelayMs = source.MaxRetryDelayMs
	opts.CacheRetention = source.CacheRetention
	opts.Headers = mergeStringMaps(source.Headers)
	opts.Metadata = mergeAnyMaps(source.Metadata)
}

func ApplyStreamOptionsPatch(opts StreamOptions, patch *StreamOptionsPatch) StreamOptions {
	if patch == nil {
		return cloneStreamOptions(opts)
	}
	out := cloneStreamOptions(opts)
	if patch.Transport != nil {
		out.Transport = *patch.Transport
	}
	if patch.TimeoutMs != nil {
		out.TimeoutMs = *patch.TimeoutMs
	}
	if patch.MaxRetries != nil {
		out.MaxRetries = *patch.MaxRetries
	}
	if patch.MaxRetryDelayMs != nil {
		out.MaxRetryDelayMs = *patch.MaxRetryDelayMs
	}
	if patch.CacheRetention != nil {
		out.CacheRetention = *patch.CacheRetention
	}
	if patch.HeadersUnset {
		out.Headers = nil
	}
	for key, value := range patch.Headers {
		if value == nil {
			delete(out.Headers, key)
			continue
		}
		if out.Headers == nil {
			out.Headers = map[string]string{}
		}
		out.Headers[key] = *value
	}
	if len(out.Headers) == 0 {
		out.Headers = nil
	}
	if patch.MetadataUnset {
		out.Metadata = nil
	}
	for key, value := range patch.Metadata {
		if value == nil {
			delete(out.Metadata, key)
			continue
		}
		if out.Metadata == nil {
			out.Metadata = map[string]any{}
		}
		out.Metadata[key] = value.V
	}
	if len(out.Metadata) == 0 {
		out.Metadata = nil
	}
	return out
}

func applyStreamOptionsPatch(opts StreamOptions, patch *StreamOptionsPatch) StreamOptions {
	return ApplyStreamOptionsPatch(opts, patch)
}

func cloneStreamOptions(opts StreamOptions) StreamOptions {
	opts.Headers = mergeStringMaps(opts.Headers)
	opts.Metadata = mergeAnyMaps(opts.Metadata)
	return opts
}
