package codingagent

import (
	"net/url"
	"strings"
)

type GitSource struct {
	Type   string `json:"type"`
	Repo   string `json:"repo"`
	Host   string `json:"host"`
	Path   string `json:"path"`
	Ref    string `json:"ref,omitempty"`
	Pinned bool   `json:"pinned"`
}

func ParseGitURL(source string) (GitSource, bool) {
	trimmed := strings.TrimSpace(source)
	hasGitPrefix := strings.HasPrefix(trimmed, "git:")
	value := trimmed
	if hasGitPrefix {
		value = strings.TrimSpace(strings.TrimPrefix(trimmed, "git:"))
	}
	if !hasGitPrefix && !hasExplicitGitProtocol(value) {
		return GitSource{}, false
	}
	if hosted, ok := parseHostedGitURL(value, hasGitPrefix); ok {
		return hosted, true
	}
	return parseGenericGitURL(value)
}

func hasExplicitGitProtocol(value string) bool {
	lower := strings.ToLower(value)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "ssh://") ||
		strings.HasPrefix(lower, "git://")
}

// hostShorthand maps hosted-git-info style scheme prefixes to their host and
// clone-URL base. Mirrors hostedGitInfo.fromUrl's built-in providers used by
// git.ts. Only applied when the source carries an explicit git: prefix.
type hostShorthand struct {
	host    string
	repoURL string // clone URL prefix, e.g. "https://github.com/"
	gist    bool   // gist paths are a bare id (project only, no user/repo)
}

var gitHostShorthands = map[string]hostShorthand{
	"github:":     {host: "github.com", repoURL: "https://github.com/"},
	"github.com/": {host: "github.com", repoURL: "https://github.com/"},
	"gitlab:":     {host: "gitlab.com", repoURL: "https://gitlab.com/"},
	"bitbucket:":  {host: "bitbucket.org", repoURL: "https://bitbucket.org/"},
	"gist:":       {host: "gist.github.com", repoURL: "https://gist.github.com/", gist: true},
}

func parseHostedGitURL(value string, hasGitPrefix bool) (GitSource, bool) {
	repo, ref := splitGitRef(value)
	for prefix, sh := range gitHostShorthands {
		if strings.HasPrefix(repo, prefix) {
			path := strings.TrimPrefix(repo, prefix)
			path = strings.Trim(strings.TrimSuffix(path, ".git"), "/")
			if sh.gist {
				// Gist sources are project-only (a bare id); they have no
				// "user/repo" path so validRepoPath does not apply.
				if path == "" || strings.Contains(path, "/") {
					continue
				}
				return GitSource{
					Type:   "git",
					Repo:   sh.repoURL + path,
					Host:   sh.host,
					Path:   path,
					Ref:    ref,
					Pinned: ref != "",
				}, true
			}
			if validRepoPath(path) {
				return GitSource{
					Type:   "git",
					Repo:   sh.repoURL + path,
					Host:   sh.host,
					Path:   path,
					Ref:    ref,
					Pinned: ref != "",
				}, true
			}
		}
	}
	// With an explicit git: prefix, a bare "user/repo" (no host dot, no protocol)
	// resolves to github.com, matching hostedGitInfo.fromUrl. Without the prefix a
	// bare "owner/repo" must stay a local path (TS isLocalPath / git_test.go:18).
	if hasGitPrefix && !strings.Contains(repo, "://") && !strings.HasPrefix(repo, "git@") {
		path := strings.Trim(strings.TrimSuffix(repo, ".git"), "/")
		if host, _, ok := strings.Cut(path, "/"); ok && !strings.Contains(host, ".") && host != "localhost" {
			if validRepoPath(path) {
				return GitSource{
					Type:   "git",
					Repo:   "https://github.com/" + path,
					Host:   "github.com",
					Path:   path,
					Ref:    ref,
					Pinned: ref != "",
				}, true
			}
		}
	}
	if strings.HasPrefix(repo, "https://github.com/") || strings.HasPrefix(repo, "http://github.com/") || strings.HasPrefix(repo, "ssh://git@github.com/") {
		parsed, err := url.Parse(repo)
		if err != nil {
			return GitSource{}, false
		}
		path := strings.Trim(strings.TrimSuffix(parsed.Path, ".git"), "/")
		if !validRepoPath(path) {
			return GitSource{}, false
		}
		return GitSource{
			Type:   "git",
			Repo:   strings.TrimSuffix(repo, "/"),
			Host:   "github.com",
			Path:   path,
			Ref:    ref,
			Pinned: ref != "",
		}, true
	}
	return GitSource{}, false
}

func parseGenericGitURL(value string) (GitSource, bool) {
	repoWithoutRef, ref := splitGitRef(value)
	repo := repoWithoutRef
	host := ""
	path := ""
	if strings.HasPrefix(repoWithoutRef, "git@") {
		rest := strings.TrimPrefix(repoWithoutRef, "git@")
		hostPart, pathPart, ok := strings.Cut(rest, ":")
		if !ok {
			return GitSource{}, false
		}
		host = hostPart
		path = pathPart
	} else if hasExplicitGitProtocol(repoWithoutRef) {
		parsed, err := url.Parse(repoWithoutRef)
		if err != nil {
			return GitSource{}, false
		}
		host = parsed.Hostname()
		path = strings.TrimPrefix(parsed.Path, "/")
	} else {
		hostPart, pathPart, ok := strings.Cut(repoWithoutRef, "/")
		if !ok || (!strings.Contains(hostPart, ".") && hostPart != "localhost") {
			return GitSource{}, false
		}
		host = hostPart
		path = pathPart
		repo = "https://" + repoWithoutRef
	}
	normalizedPath := strings.Trim(strings.TrimSuffix(path, ".git"), "/")
	if host == "" || !validRepoPath(normalizedPath) {
		return GitSource{}, false
	}
	return GitSource{
		Type:   "git",
		Repo:   strings.TrimSuffix(repo, "/"),
		Host:   host,
		Path:   normalizedPath,
		Ref:    ref,
		Pinned: ref != "",
	}, true
}

func splitGitRef(value string) (string, string) {
	if before, after, ok := strings.Cut(value, "#"); ok && before != "" && after != "" {
		return before, after
	}
	if strings.HasPrefix(value, "git@") {
		hostPart, pathPart, ok := strings.Cut(value, ":")
		if !ok {
			return value, ""
		}
		repoPath, ref, ok := strings.Cut(pathPart, "@")
		if !ok || repoPath == "" || ref == "" {
			return value, ""
		}
		return hostPart + ":" + repoPath, ref
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return value, ""
		}
		path := strings.TrimPrefix(parsed.Path, "/")
		repoPath, ref, ok := strings.Cut(path, "@")
		if !ok || repoPath == "" || ref == "" {
			return value, ""
		}
		parsed.Path = "/" + repoPath
		parsed.RawPath = ""
		return strings.TrimSuffix(parsed.String(), "/"), ref
	}
	host, pathPart, ok := strings.Cut(value, "/")
	if !ok {
		return value, ""
	}
	repoPath, ref, ok := strings.Cut(pathPart, "@")
	if !ok || repoPath == "" || ref == "" {
		return value, ""
	}
	return host + "/" + repoPath, ref
}

func validRepoPath(path string) bool {
	parts := strings.Split(path, "/")
	return len(parts) >= 2 && parts[0] != "" && parts[1] != ""
}
