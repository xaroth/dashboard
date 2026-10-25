package main

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// healthz checks /healthz of the admin listener at addr. The image's
// HEALTHCHECK runs it, so a check does not start a Bun runtime.
func healthz(ctx context.Context, addr string) error {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz: %s", resp.Status)
	}
	return nil
}
