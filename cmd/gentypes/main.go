// Command gentypes writes TypeScript types for every module query's params
// and result (queries.ts) and for the page props (props.ts), so pages are
// type-checked against the Go structs.
//
//	go run ./cmd/gentypes [-dir pages/generated]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/xaroth/dashboard/modules"
)

func main() {
	dir := flag.String("dir", "pages/generated", "output directory")
	flag.Parse()
	if err := run(*dir); err != nil {
		fmt.Fprintln(os.Stderr, "gentypes:", err)
		os.Exit(1)
	}
}

func run(dir string) error {
	queries, err := generate(modules.All())
	if err != nil {
		return err
	}
	props, err := generateProps(propsTypes, propsEnums)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "queries.ts"), queries, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "props.ts"), props, 0o644)
}
