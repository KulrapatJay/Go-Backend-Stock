package service

import (
	_ "embed" // for the go:embed directive below
	"strings"
)

// embeddedBlocklist is internal/core/service/blocklist.txt, built by scripts/build_blocklist.go
// from the source named in NOTICE. It is compiled into the binary so the check needs no network
// and no file at runtime.
//
//go:embed blocklist.txt
var embeddedBlocklist string

// DefaultBlocklist loads the bundled list of common passwords. Call it once at startup.
func DefaultBlocklist() (*Blocklist, error) {
	return NewBlocklist(strings.NewReader(embeddedBlocklist))
}
