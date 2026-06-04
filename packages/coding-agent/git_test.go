package codingagent

import "testing"

func TestParseGitURL(t *testing.T) {
	tests := []struct {
		source string
		repo   string
		host   string
		path   string
		ref    string
		ok     bool
	}{
		{"git:github:owner/repo#main", "https://github.com/owner/repo", "github.com", "owner/repo", "main", true},
		{"https://github.com/owner/repo.git#v1", "https://github.com/owner/repo.git", "github.com", "owner/repo", "v1", true},
		{"git:git@gitlab.example.com:team/repo@feature", "git@gitlab.example.com:team/repo", "gitlab.example.com", "team/repo", "feature", true},
		{"git:git.example.com/team/repo@abc", "https://git.example.com/team/repo", "git.example.com", "team/repo", "abc", true},
		{"owner/repo", "", "", "", "", false},
		// Hosted-git-info shorthands: only resolved with an explicit git: prefix.
		{"git:owner/repo", "https://github.com/owner/repo", "github.com", "owner/repo", "", true},
		{"git:owner/repo#dev", "https://github.com/owner/repo", "github.com", "owner/repo", "dev", true},
		{"git:gitlab:team/repo", "https://gitlab.com/team/repo", "gitlab.com", "team/repo", "", true},
		{"git:bitbucket:team/repo#v2", "https://bitbucket.org/team/repo", "bitbucket.org", "team/repo", "v2", true},
		{"git:gist:abc123", "https://gist.github.com/abc123", "gist.github.com", "abc123", "", true},
	}
	for _, tt := range tests {
		got, ok := ParseGitURL(tt.source)
		if ok != tt.ok {
			t.Fatalf("%s ok=%v", tt.source, ok)
		}
		if !ok {
			continue
		}
		if got.Repo != tt.repo || got.Host != tt.host || got.Path != tt.path || got.Ref != tt.ref || got.Pinned != (tt.ref != "") {
			t.Fatalf("%s got=%#v", tt.source, got)
		}
	}
}

func TestParsePackageSourceUsesGitParser(t *testing.T) {
	parsed := ParsePackageSource("git:github:owner/repo#main")
	if parsed.Kind != "git" || parsed.Name != "https://github.com/owner/repo" || parsed.Ref != "main" || !parsed.Pinned {
		t.Fatalf("parsed=%#v", parsed)
	}
	parsed = ParsePackageSource("npm:@scope/pkg@1.2.3")
	if parsed.Kind != "npm" || parsed.Name != "@scope/pkg" || parsed.Ref != "1.2.3" {
		t.Fatalf("npm parsed=%#v", parsed)
	}
}

func TestParsePackageSourceGitShorthands(t *testing.T) {
	for _, src := range []string{"git:owner/repo", "git:gitlab:team/repo", "git:bitbucket:team/repo", "git:gist:abc123"} {
		parsed := ParsePackageSource(src)
		if parsed.Kind != "git" {
			t.Fatalf("%s: Kind=%q, want git (parsed=%#v)", src, parsed.Kind, parsed)
		}
	}
	// A bare "owner/repo" without the git: prefix must remain a local path.
	if got := ParsePackageSource("owner/repo"); got.Kind == "git" {
		t.Fatalf("bare owner/repo should not be git, got %#v", got)
	}
}
