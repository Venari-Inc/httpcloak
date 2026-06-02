package session

import "testing"

// Locks Chrome's default strict-origin-when-cross-origin Referer policy for the
// redirect path (issue #70): full URL same-origin, origin-only cross-origin
// (trailing slash), omitted on an https->http downgrade.
func TestRedirectReferer(t *testing.T) {
	cases := []struct {
		name        string
		prevURL     string
		downgrade   bool
		crossOrigin bool
		want        string
	}{
		{"same-origin keeps full URL", "https://example.com/a/b?q=1", false, false, "https://example.com/a/b?q=1"},
		{"cross-origin -> origin only", "https://example.com/a/b?q=1", false, true, "https://example.com/"},
		{"cross-origin non-default port kept", "https://example.com:8443/x", false, true, "https://example.com:8443/"},
		{"cross-origin default https port dropped", "https://example.com:443/x", false, true, "https://example.com/"},
		{"https->http downgrade omits", "https://example.com/a", true, true, ""},
		// Chrome never leaks the fragment or credentials in Referer, and drops a
		// redundant default port from the same-origin document URL.
		{"same-origin strips fragment", "https://example.com/a#section", false, false, "https://example.com/a"},
		{"same-origin strips credentials", "https://user:pass@example.com/a", false, false, "https://example.com/a"},
		{"same-origin strips creds+fragment keeps query", "https://user:pass@example.com/a?q=1#f", false, false, "https://example.com/a?q=1"},
		{"same-origin https default port dropped", "https://example.com:443/a", false, false, "https://example.com/a"},
		{"http same-origin default port dropped", "http://example.com:80/a", false, false, "http://example.com/a"},
		{"http same-origin non-default port kept", "http://example.com:8080/a", false, false, "http://example.com:8080/a"},
		{"http->https upgrade cross-origin sends http origin", "http://example.com/a", false, true, "http://example.com/"},
		{"cross-origin strips creds+fragment keeps port", "https://user:pass@example.com:8443/a#f", false, true, "https://example.com:8443/"},
		{"http cross-origin default port dropped", "http://example.com:80/a", false, true, "http://example.com/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := redirectReferer(c.prevURL, c.downgrade, c.crossOrigin)
			if got != c.want {
				t.Fatalf("redirectReferer(%q, downgrade=%v, cross=%v) = %q, want %q", c.prevURL, c.downgrade, c.crossOrigin, got, c.want)
			}
		})
	}
}
