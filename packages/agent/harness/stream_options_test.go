package harness

import "testing"

func TestApplyStreamOptionsPatch(t *testing.T) {
	transport := "http"
	timeout := 42
	idleTimeout := 84
	header := "next"
	meta := AnyValue{V: nil}
	opts := StreamOptions{
		Transport: "auto",
		Headers:   map[string]string{"keep": "yes", "drop": "old"},
		Metadata:  map[string]any{"keep": "yes", "drop": "old"},
	}
	patched := ApplyStreamOptionsPatch(opts, &StreamOptionsPatch{
		Transport:     &transport,
		TimeoutMs:     &timeout,
		IdleTimeoutMs: &idleTimeout,
		Headers: map[string]*string{
			"drop": nil,
			"new":  &header,
		},
		Metadata: map[string]*AnyValue{
			"drop": nil,
			"nil":  &meta,
		},
	})
	if patched.Transport != "http" || patched.TimeoutMs != 42 || patched.IdleTimeoutMs != 84 {
		t.Fatalf("patched=%#v", patched)
	}
	if patched.Headers["keep"] != "yes" || patched.Headers["new"] != "next" {
		t.Fatalf("headers=%#v", patched.Headers)
	}
	if _, ok := patched.Headers["drop"]; ok {
		t.Fatalf("drop header still present: %#v", patched.Headers)
	}
	if patched.Metadata["keep"] != "yes" {
		t.Fatalf("metadata=%#v", patched.Metadata)
	}
	if value, ok := patched.Metadata["nil"]; !ok || value != nil {
		t.Fatalf("nil metadata=%#v ok=%v", value, ok)
	}
	if _, ok := patched.Metadata["drop"]; ok {
		t.Fatalf("drop metadata still present: %#v", patched.Metadata)
	}
	if opts.Headers["drop"] != "old" || opts.Metadata["drop"] != "old" {
		t.Fatalf("input mutated headers=%#v metadata=%#v", opts.Headers, opts.Metadata)
	}
}

func TestApplyStreamOptionsPatchUnsetMaps(t *testing.T) {
	patched := ApplyStreamOptionsPatch(StreamOptions{
		Headers:  map[string]string{"x": "y"},
		Metadata: map[string]any{"a": "b"},
	}, &StreamOptionsPatch{
		HeadersUnset:  true,
		MetadataUnset: true,
	})
	if patched.Headers != nil || patched.Metadata != nil {
		t.Fatalf("patched=%#v", patched)
	}
}
