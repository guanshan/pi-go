package tools

import (
	"context"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// TestSearchRipgrepMatchesRE2Format locks the core parity guarantee: the
// ripgrep path produces output byte-identical to the RE2 fallback, so swapping
// engines (rg present vs absent) never changes what the model sees.
func TestSearchRipgrepMatchesRE2Format(t *testing.T) {
	rg := resolveManagedTool(context.Background(), managedToolRG, "").Path
	if rg == "" {
		t.Skip("rg not available on PATH")
	}
	root := t.TempDir()
	writeFile(t, root, "sub/file.go", "line1\nNEEDLE here\nline3\nNEEDLE again\n")
	q := grepQuery{pattern: "NEEDLE", limit: 100, context: 1}

	rgResults, rgLimit, rgTrunc, err := searchRipgrep(context.Background(), rg, root, true, q)
	if err != nil {
		t.Fatalf("searchRipgrep error: %v", err)
	}
	re2Results, re2Limit, re2Trunc := searchRE2(regexp.MustCompile("NEEDLE"), root, true, q)
	if !reflect.DeepEqual(rgResults, re2Results) {
		t.Fatalf("rg vs RE2 output diverged\n rg:  %#v\nRE2: %#v", rgResults, re2Results)
	}
	if rgLimit != re2Limit || rgTrunc != re2Trunc {
		t.Fatalf("flag mismatch: rg(%v,%v) re2(%v,%v)", rgLimit, rgTrunc, re2Limit, re2Trunc)
	}
}

// TestGrepFallsBackToRE2WhenNoRipgrep locks the ripgrep-free fallback: matches
// still format identically, and patterns RE2 cannot compile surface an
// explanatory error instead of silently mismatching.
func TestGrepFallsBackToRE2WhenNoRipgrep(t *testing.T) {
	old := ripgrepFinder
	ripgrepFinder = func(context.Context, string) managedToolResult { return managedToolResult{} }
	defer func() { ripgrepFinder = old }()

	root := t.TempDir()
	writeFile(t, root, "a.txt", "x\nNEEDLE\ny\n")
	res := GrepTool{CWD: root}.Execute(context.Background(), raw(map[string]any{"pattern": "NEEDLE"}), nil)
	if got := toolText(res.Content); got != "a.txt:2: NEEDLE" {
		t.Fatalf("RE2 fallback format mismatch\n got: %q\nwant: %q", got, "a.txt:2: NEEDLE")
	}

	advanced := GrepTool{CWD: root}.Execute(context.Background(), raw(map[string]any{"pattern": "foo(?=bar)"}), nil)
	if !advanced.IsError {
		t.Fatal("RE2 fallback should error on a look-around pattern")
	}
	if msg := toolText(advanced.Content); strings.Contains(msg, "ripgrep") || strings.Contains(msg, "RE2") {
		t.Fatalf("fallback reason should be hidden when no bin dir is configured, got: %q", msg)
	}

	managed := GrepTool{CWD: root, BinDir: t.TempDir()}.Execute(context.Background(), raw(map[string]any{"pattern": "foo(?=bar)"}), nil)
	if !managed.IsError {
		t.Fatal("RE2 fallback with managed bin dir should still error on a look-around pattern")
	}
	if msg := toolText(managed.Content); !strings.Contains(msg, "ripgrep") || !strings.Contains(msg, "RE2") {
		t.Fatalf("managed fallback error should explain the engine fallback, got: %q", msg)
	}
}

// TestSearchRE2FallbackContextFormat unit-tests the RE2 path's match/context
// formatting directly so it stays correct even though the default execution path
// now prefers ripgrep.
func TestSearchRE2FallbackContextFormat(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "line1\nNEEDLE\nline3\n")
	re := regexp.MustCompile("NEEDLE")
	results, matchLimit, linesTruncated := searchRE2(re, root, true, grepQuery{limit: 100, context: 1})
	want := []string{"a.txt-1- line1", "a.txt:2: NEEDLE", "a.txt-3- line3"}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("searchRE2 context format mismatch\n got: %#v\nwant: %#v", results, want)
	}
	if matchLimit || linesTruncated {
		t.Fatalf("unexpected flags: matchLimit=%v linesTruncated=%v", matchLimit, linesTruncated)
	}
}
