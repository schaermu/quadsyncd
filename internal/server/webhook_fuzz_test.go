package server

import "testing"

func FuzzRepoFullNameFromURL(f *testing.F) {
	// Seed corpus with known URL formats.
	f.Add("https://github.com/owner/repo.git")
	f.Add("git@github.com:owner/repo.git")
	f.Add("ssh://git@github.com/owner/repo.git")
	f.Add("http://github.com/owner/repo")
	f.Add("")
	f.Add("not-a-url")
	f.Add("://missing-scheme")
	f.Add("git@:no-path")
	f.Add("git@github.com:")
	f.Add("https://github.com/repo-only")

	f.Fuzz(func(_ *testing.T, url string) {
		// Should never panic regardless of input.
		_ = repoFullNameFromURL(url)
	})
}

func FuzzVerifySignature(f *testing.F) {
	f.Add([]byte("body"), "sha256=abc123")
	f.Add([]byte(""), "")
	f.Add([]byte("body"), "invalid-prefix")
	f.Add([]byte("body"), "sha256=")
	f.Add([]byte("payload"), "sha256=deadbeef")
	f.Add([]byte{0, 1, 2, 3}, "sha256=0000")

	s := &Server{secret: []byte("test-secret")}
	f.Fuzz(func(_ *testing.T, body []byte, signature string) {
		// Should never panic regardless of input.
		_ = s.verifySignature(body, signature)
	})
}
