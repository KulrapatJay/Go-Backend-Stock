//go:build ignore

// Command build_blocklist filters a list of common passwords down to the entries that can match
// a password our rules accept (service.FilterBlocklistEntry), removes duplicates and writes the
// sorted result. See internal/core/service/NOTICE for the source list and how it was fetched.
//
//	go run scripts/build_blocklist.go -in /path/to/source.txt -out internal/core/service/blocklist.txt
package main

import (
	"flag"
	"fmt"
	"os"

	"go-backend-stock/internal/core/service"
)

func main() {
	in := flag.String("in", "", "source list of common passwords, one per line")
	out := flag.String("out", "internal/core/service/blocklist.txt", "filtered blocklist to write")
	flag.Parse()
	if *in == "" {
		fmt.Fprintln(os.Stderr, "-in is required")
		os.Exit(2)
	}
	if err := run(*in, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(inPath, outPath string) error {
	src, err := os.Open(inPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(outPath)
	if err != nil {
		return err
	}
	kept, buildErr := service.BuildBlocklist(src, dst)
	closeErr := dst.Close()
	if buildErr != nil {
		return buildErr
	}
	if closeErr != nil {
		return closeErr
	}
	fmt.Printf("wrote %d entries to %s\n", kept, outPath)
	return nil
}
