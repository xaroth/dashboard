// Command gentypes writes TypeScript types for every registered query's
// params and result, so pages are type-checked against the Go structs.
//
//	go run ./cmd/gentypes [-out pages/generated/queries.ts]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/xaroth/dashboard/modules"
	"github.com/xaroth/dashboard/service/modules"
)

func main() {
	out := flag.String("out", "pages/generated/queries.ts", "output file")
	flag.Parse()
	src, err := generate(modules.Registered())
	if err != nil {
		fmt.Fprintln(os.Stderr, "gentypes:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "gentypes:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gentypes:", err)
		os.Exit(1)
	}
}
