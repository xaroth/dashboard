// Command preview renders the sample template for each panel and writes the PNGs to a directory.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/xaroth/dashboard/render"
)

func main() {
	bin := flag.String("renderer", "renderer/target/release/renderer", "path to the renderer binary")
	out := flag.String("out", "out", "output directory")
	flag.Parse()

	if err := run(context.Background(), *bin, *out); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, bin, out string) error {
	tmpl, err := template.ParseFiles("templates/sample.html")
	if err != nil {
		return fmt.Errorf("parsing template: %w", err)
	}
	r := &render.Renderer{
		Bin: bin,
		CSS: "templates/style.css",
		Fonts: []string{
			"fonts/Inter-Regular.ttf",
			"fonts/Inter-SemiBold.ttf",
			"fonts/Inter-Bold.ttf",
		},
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", out, err)
	}

	panels := []struct {
		name  string
		panel render.Panel
	}{
		{"e1001", render.E1001},
		{"e1003", render.E1003},
	}
	for _, p := range panels {
		var html bytes.Buffer
		if err := tmpl.Execute(&html, map[string]string{"Class": p.name}); err != nil {
			return fmt.Errorf("executing template for %s: %w", p.name, err)
		}

		start := time.Now()
		img, err := r.Render(ctx, html.String(), p.panel)
		if err != nil {
			return fmt.Errorf("rendering %s: %w", p.name, err)
		}
		elapsed := time.Since(start)

		data, err := render.Encode(img)
		if err != nil {
			return fmt.Errorf("encoding %s: %w", p.name, err)
		}
		path := filepath.Join(out, p.name+".png")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		fmt.Printf("%s: %d bytes, rendered in %s\n", path, len(data), elapsed.Round(time.Millisecond))
	}
	return nil
}
