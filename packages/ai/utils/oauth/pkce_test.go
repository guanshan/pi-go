package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"testing"
)

var base64URLNoPad = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func TestGeneratePKCEProducesValidChallenge(t *testing.T) {
	pair, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if pair.Verifier == "" || pair.Challenge == "" {
		t.Fatalf("empty PKCE pair: %#v", pair)
	}
	if !base64URLNoPad.MatchString(pair.Verifier) {
		t.Fatalf("verifier not base64url-no-pad: %q", pair.Verifier)
	}
	if !base64URLNoPad.MatchString(pair.Challenge) {
		t.Fatalf("challenge not base64url-no-pad: %q", pair.Challenge)
	}
	// Challenge must be the S256 hash of the verifier, per RFC 7636.
	want := sha256.Sum256([]byte(pair.Verifier))
	if got := base64.RawURLEncoding.EncodeToString(want[:]); got != pair.Challenge {
		t.Fatalf("challenge=%q, want S256(verifier)=%q", pair.Challenge, got)
	}
}

func TestGeneratePKCEUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		pair, err := GeneratePKCE()
		if err != nil {
			t.Fatal(err)
		}
		if seen[pair.Verifier] {
			t.Fatalf("duplicate PKCE verifier on iteration %d", i)
		}
		seen[pair.Verifier] = true
	}
}

func TestRandomHexLengthAndAlphabet(t *testing.T) {
	hex, err := RandomHex(16)
	if err != nil {
		t.Fatal(err)
	}
	if len(hex) != 32 { // 16 bytes -> 32 hex chars
		t.Fatalf("RandomHex(16) length=%d, want 32", len(hex))
	}
	if !regexp.MustCompile(`^[0-9a-f]+$`).MatchString(hex) {
		t.Fatalf("RandomHex output not lowercase hex: %q", hex)
	}
	a, _ := RandomHex(16)
	b, _ := RandomHex(16)
	if a == b {
		t.Fatal("RandomHex produced identical values")
	}
}
