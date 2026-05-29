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
