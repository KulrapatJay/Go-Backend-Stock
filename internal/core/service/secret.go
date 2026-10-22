package service

import (
	"bytes"
	"fmt"
)

// minSecretBytes is the shortest HMAC secret the helpers accept (config enforces the same).
const minSecretBytes = 32

// copySecret checks the length of an HMAC secret and returns a private copy. The error names the
// secret but never contains its value.
func copySecret(name string, secret []byte) ([]byte, error) {
	if len(secret) < minSecretBytes {
		return nil, fmt.Errorf("%s must be at least %d bytes", name, minSecretBytes)
	}
	return bytes.Clone(secret), nil
}
