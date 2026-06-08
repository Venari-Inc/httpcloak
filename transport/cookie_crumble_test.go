package transport

import (
	"reflect"
	"testing"
)

// TestCrumbleCookie locks crumbleCookie to Chromium's exact CookieToCrumbs /
// QPACK ValueSplittingHeaderList boundary semantics: trim outer ' '/'\t', split
// on each ';', drop exactly one optional space after each ';'.
func TestCrumbleCookie(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"whitespace only", "  \t ", nil},
		{"single pair", "a=1", []string{"a=1"}},
		{"jar join one space", "a=1; b=2; c=3", []string{"a=1", "b=2", "c=3"}},
		{"no space after semicolon", "a=1;b=2;c=3", []string{"a=1", "b=2", "c=3"}},
		{"two spaces keeps one", "a=1;  b=2", []string{"a=1", " b=2"}},
		{"outer whitespace trimmed", "  a=1; b=2  ", []string{"a=1", "b=2"}},
		{"tab outer trimmed", "\ta=1; b=2\t", []string{"a=1", "b=2"}},
		{"trailing semicolon yields empty tail", "a=1;", []string{"a=1", ""}},
		{"value with equals and chars", "sid=ab.cd-ef_gh; token=xyz%20", []string{"sid=ab.cd-ef_gh", "token=xyz%20"}},
		{"single with internal spaces preserved", "a=hello world", []string{"a=hello world"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := crumbleCookie(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("crumbleCookie(%q) = %#v, want %#v", c.in, got, c.want)
			}
		})
	}
}
