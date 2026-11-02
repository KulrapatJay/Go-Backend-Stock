package service

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"
)

func TestNewOIDCValue(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		v, err := NewOIDCValue()
		if err != nil {
			t.Fatal(err)
		}
		if len(v) != 43 || !ValidOIDCValue(v) || strings.ContainsAny(v, "+/=") {
			t.Fatalf("value %q is not 43 unpadded base64url characters", v)
		}
		if seen[v] {
			t.Fatal("NewOIDCValue repeated a value")
		}
		seen[v] = true
	}
}

func TestValidOIDCValueRejectsOtherShapes(t *testing.T) {
	good, err := NewOIDCValue()
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{
		"empty": "", "short": good[:42], "long": good + "A", "padding": good[:42] + "=",
		"standard base64": strings.Repeat("+", 43), "space": " " + good[1:],
	} {
		if ValidOIDCValue(v) {
			t.Errorf("%s %q accepted", name, v)
		}
	}
}

func TestHashOIDCValue(t *testing.T) {
	sum := sha256.Sum256([]byte("state-value"))
	if got := HashOIDCValue("state-value"); !bytes.Equal(got, sum[:]) || len(got) != 32 {
		t.Errorf("HashOIDCValue = %x, want the SHA-256 of the string", got)
	}
}

// RFC 7636 appendix B.
func TestCodeChallengeMatchesRFC7636(t *testing.T) {
	if got := CodeChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Errorf("CodeChallenge = %q", got)
	}
}

func TestVerifierIsBoundToStateBindingAndSecret(t *testing.T) {
	c, err := NewOIDCCodec(bytes.Repeat([]byte("i"), 32))
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewOIDCCodec(bytes.Repeat([]byte("j"), 32))
	if err != nil {
		t.Fatal(err)
	}
	v := c.Verifier("state-a", "binding-a")
	if len(v) != 43 || v != c.Verifier("state-a", "binding-a") {
		t.Fatalf("Verifier = %q, want 43 characters and the same value each time", v)
	}
	for name, w := range map[string]string{
		"other state":   c.Verifier("state-b", "binding-a"),
		"other binding": c.Verifier("state-a", "binding-b"),
		"other secret":  other.Verifier("state-a", "binding-a"),
		"moved byte":    c.Verifier("state-ab", "inding-a"),
	} {
		if w == v {
			t.Errorf("%s gives the same verifier", name)
		}
	}
	if _, err := NewOIDCCodec(bytes.Repeat([]byte("i"), 31)); err == nil {
		t.Error("NewOIDCCodec accepted a 31-byte secret")
	}
}
