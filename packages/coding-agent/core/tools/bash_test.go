package tools

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestBashToolBasicOutput(t *testing.T) {
	tool := BashTool{CWD: t.TempDir()}
	result := tool.Execute(context.Background(), raw(map[string]any{"command": "echo hello"}), nil)
	if result.IsError {
		t.Fatalf("unexpected error: %s", toolText(result.Content))
	}
	if got := strings.TrimSpace(toolText(result.Content)); got != "hello" {
		t.Fatalf("output=%q", got)
	}
}

func TestBashToolDoesNotRunLoginStartupFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/.bash_profile", []byte("echo startup\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	t.Setenv("BASH_ENV", "")
	tool := BashTool{CWD: dir}
	result := tool.Execute(context.Background(), raw(map[string]any{"command": "printf body"}), nil)
	if result.IsError {
		t.Fatalf("unexpected error: %s", toolText(result.Content))
	}
	if got := strings.TrimSpace(toolText(result.Content)); got != "body" {
		t.Fatalf("output=%q", got)
	}
}

func TestBashToolStreamsUpdates(t *testing.T) {
	tool := BashTool{CWD: t.TempDir()}
	var mu sync.Mutex
	var updates []string
	onUpdate := func(partial ai.ToolResult) {
		mu.Lock()
		updates = append(updates, toolText(partial.Content))
		mu.Unlock()
	}
	// Emit several lines with delays so throttled updates fire more than once.
	result := tool.Execute(context.Background(), raw(map[string]any{
		"command": "for i in 1 2 3 4 5; do echo line$i; sleep 0.12; done",
	}), onUpdate)
	if result.IsError {
		t.Fatalf("unexpected error: %s", toolText(result.Content))
	}
	mu.Lock()
	count := len(updates)
	mu.Unlock()
	if count < 2 {
		t.Fatalf("expected multiple streaming updates, got %d: %#v", count, updates)
	}
	if !strings.Contains(toolText(result.Content), "line5") {
		t.Fatalf("final output missing last line: %q", toolText(result.Content))
	}
}

func TestBashToolTimeout(t *testing.T) {
	tool := BashTool{CWD: t.TempDir()}
	start := time.Now()
	// seq completes and flushes its output to the pipe before the sleep, so the
	// partial output survives the SIGKILL (a single buffered echo would not).
	result := tool.Execute(context.Background(), raw(map[string]any{
		"command": "seq 1 2000; sleep 30",
		"timeout": 3.0,
	}), nil)
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("timeout did not fire promptly: %s", elapsed)
	}
	if !result.IsError {
		t.Fatal("expected timeout to be an error")
	}
	text := toolText(result.Content)
	if !strings.Contains(text, "timed out") {
		t.Fatalf("missing timeout message: %q", text)
	}
	if !strings.Contains(text, "2000") {
		t.Fatalf("partial output not preserved on timeout: %q", lastLines(text, 3))
	}
}

func TestBashToolAbort(t *testing.T) {
	tool := BashTool{CWD: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Abort only once output has actually been produced so the partial-output
	// assertion is stable.
	var once sync.Once
	onUpdate := func(p ai.ToolResult) {
		if strings.TrimSpace(toolText(p.Content)) != "" {
			once.Do(func() { go cancel() })
		}
	}
	start := time.Now()
	result := tool.Execute(ctx, raw(map[string]any{"command": "seq 1 2000; sleep 30"}), onUpdate)
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("abort did not terminate promptly: %s", elapsed)
	}
	if !result.IsError {
		t.Fatal("expected abort to be an error")
	}
	text := toolText(result.Content)
	if !strings.Contains(text, "Command aborted") {
		t.Fatalf("missing abort message: %q", text)
	}
	if !strings.Contains(text, "2000") {
		t.Fatalf("partial output not preserved on abort: %q", lastLines(text, 3))
	}
}

func TestBashToolKillsProcessGroupOnAbort(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("requires unix shell")
	}
	dir := t.TempDir()
	pidFile := dir + "/child.pid"
	tool := BashTool{CWD: dir}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Abort once the child pid file has been written, so we know the detached
	// child is actually running.
	go func() {
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(pidFile); err == nil {
				time.Sleep(50 * time.Millisecond)
				cancel()
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
	}()
	// Start a detached child that outlives the shell unless the whole process
	// group is killed. It records its pid so the test can verify it died.
	tool.Execute(ctx, raw(map[string]any{
		"command": "sleep 30 & echo $! > " + pidFile + "; wait",
	}), nil)

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("child pid file not written: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("bad pid: %v", err)
	}
	// Poll: the background child should be reaped once the group is killed.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return // process gone — group kill worked
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Clean up the stray child if it survived.
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("background child %d survived abort; process group was not killed", pid)
}

func TestBashToolTruncationWritesFullOutput(t *testing.T) {
	tool := BashTool{CWD: t.TempDir()}
	totalLines := DefaultMaxLines + 500
	result := tool.Execute(context.Background(), raw(map[string]any{
		"command": "seq 1 " + strconv.Itoa(totalLines),
	}), nil)
	if result.IsError {
		t.Fatalf("unexpected error: %s", toolText(result.Content))
	}
	text := toolText(result.Content)
	if !strings.Contains(text, "Full output:") {
		t.Fatalf("expected full-output footer, got tail: %q", lastLines(text, 3))
	}
	if !strings.Contains(text, "of "+strconv.Itoa(totalLines)) {
		t.Fatalf("footer missing total line count: %q", lastLines(text, 3))
	}
	details, ok := result.Details.(map[string]any)
	if !ok {
		t.Fatalf("expected details map, got %T", result.Details)
	}
	pathAny, ok := details["fullOutputPath"]
	if !ok {
		t.Fatal("missing fullOutputPath in details")
	}
	path, _ := pathAny.(string)
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("full output file not readable: %v", err)
	}
	if !strings.Contains(string(full), strconv.Itoa(totalLines)) {
		t.Fatal("full output file does not contain the last line")
	}
	_ = os.Remove(path)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
