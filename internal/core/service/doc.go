// Package service holds the core's use cases (added from milestone 4b) and the security helpers
// they share: password hashing and policy, OTP, CSRF and session tokens, rate-limit keys and
// response pacing. It depends only on the standard library, golang.org/x/crypto/argon2 and the
// core's own packages.
package service
