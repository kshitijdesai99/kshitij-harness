package memory

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// RepoScope names a repo by its Git remote, so moved folders and other
// clones share memory. Without a remote it falls back to the Git root, or
// the folder itself outside Git.
func RepoScope(dir string) string {
	if out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output(); err == nil {
		if r := normalizeRemote(strings.TrimSpace(string(out))); r != "" {
			return r
		}
	}
	if root, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output(); err == nil {
		dir = strings.TrimSpace(string(root))
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// normalizeRemote makes SSH and HTTPS forms of one remote equal and drops
// credentials, which must never be stored or exported:
// git@github.com:a/b.git and https://user:token@github.com/a/b -> github.com/a/b
func normalizeRemote(url string) string {
	if url == "" || strings.HasPrefix(url, "/") || strings.HasPrefix(url, ".") || strings.HasPrefix(url, "file://") {
		return "" // local path remotes say nothing about identity
	}
	if _, rest, ok := strings.Cut(url, "://"); ok {
		url = rest
	} else if host, path, ok := strings.Cut(url, ":"); ok { // scp-like ssh
		url = host + "/" + path
	}
	if at := strings.LastIndex(url, "@"); at >= 0 && at < strings.Index(url+"/", "/") {
		url = url[at+1:]
	}
	host, path, _ := strings.Cut(url, "/")
	if i := strings.LastIndex(host, ":"); i >= 0 { // a port is not part of the repo's name
		host = host[:i]
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "" || path == "" {
		return ""
	}
	return strings.ToLower(host) + "/" + path
}

// Owner is the Git user's email, so shared notes say who wrote them.
func Owner(dir string) string {
	out, err := exec.Command("git", "-C", dir, "config", "user.email").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
