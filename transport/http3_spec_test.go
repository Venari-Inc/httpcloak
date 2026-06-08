package transport

import (
	"fmt"
	"testing"

	"github.com/sardanioss/httpcloak/dns"
	"github.com/sardanioss/httpcloak/fingerprint"
)

// The H3 transport regenerates the ClientHelloSpec per dial (the fix for the
// concurrent-dial data race: utls ApplyPreset mutates the spec in place, so a
// shared cached spec races under concurrency). This regression test locks the
// two properties that fix depends on:
//
//  1. each call returns a DISTINCT spec object (structural race-safety: no two
//     concurrent dials ever hold the same mutable spec), and
//  2. the extension and cipher-suite ORDER is byte-identical across calls, so
//     JA3/JA4 stay stable between connections of one session.
func TestH3SpecRegenDeterministic(t *testing.T) {
	tr, err := NewHTTP3Transport(fingerprint.Chrome146(), dns.NewCache())
	if err != nil {
		t.Fatalf("NewHTTP3Transport: %v", err)
	}

	s1 := tr.getSpecForHost("example.com")
	s2 := tr.getSpecForHost("example.com")
	if s1 == nil || s2 == nil {
		t.Fatal("getSpecForHost returned nil")
	}

	// (1) distinct objects: a shared pointer would mean concurrent dials race.
	if s1 == s2 {
		t.Fatal("getSpecForHost returned the SAME spec pointer twice; concurrent dials would race-mutate it")
	}

	// (2a) extension order identical (drives JA4).
	if len(s1.Extensions) != len(s2.Extensions) {
		t.Fatalf("extension count drift: %d vs %d", len(s1.Extensions), len(s2.Extensions))
	}
	for i := range s1.Extensions {
		a, b := fmt.Sprintf("%T", s1.Extensions[i]), fmt.Sprintf("%T", s2.Extensions[i])
		if a != b {
			t.Fatalf("extension order drift at %d: %s vs %s (JA4 would change between connections)", i, a, b)
		}
	}

	// (2b) cipher-suite order identical (drives JA3).
	if len(s1.CipherSuites) != len(s2.CipherSuites) {
		t.Fatalf("cipher count drift: %d vs %d", len(s1.CipherSuites), len(s2.CipherSuites))
	}
	for i := range s1.CipherSuites {
		if s1.CipherSuites[i] != s2.CipherSuites[i] {
			t.Fatalf("cipher order drift at %d: %#x vs %#x", i, s1.CipherSuites[i], s2.CipherSuites[i])
		}
	}
}
