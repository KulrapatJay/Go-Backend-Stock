package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestSecretNeverPrintsWithAnyVerb(t *testing.T) {
	s := Secret("S3cret-ZQXJ-value")
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x", "%X"} {
		if out := fmt.Sprintf(verb, s); strings.Contains(out, "ZQXJ") {
			t.Errorf("%s printed %q", verb, out)
		}
	}
	if s.Reveal() != "S3cret-ZQXJ-value" {
		t.Error("Reveal must still return the value")
	}
}
