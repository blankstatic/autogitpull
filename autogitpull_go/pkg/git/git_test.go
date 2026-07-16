package git

import "testing"

func TestRemoteWebURL(t *testing.T) {
	tests := map[string]string{
		"git@github.com:owner/repo.git":         "https://github.com/owner/repo",
		"ssh://git@gitlab.com/owner/repo.git":   "https://gitlab.com/owner/repo",
		"https://github.com/owner/repo.git":     "https://github.com/owner/repo",
		"https://example.com/owner/repo?view=1": "https://example.com/owner/repo?view=1",
		"/tmp/local-repo":                       "",
		"file:///tmp/local-repo":                "",
	}
	for remote, want := range tests {
		t.Run(remote, func(t *testing.T) {
			if got := RemoteWebURL(remote); got != want {
				t.Fatalf("RemoteWebURL(%q) = %q, want %q", remote, got, want)
			}
		})
	}
}
