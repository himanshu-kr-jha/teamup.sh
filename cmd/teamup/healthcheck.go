package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// runHealthcheck is the container's own health probe. The image is distroless,
// so there is no wget or curl for a compose healthcheck to call, and every
// probe written that way fails for ever with "executable file not found".
func runHealthcheck(_ *cobra.Command, _ []string) error {
	port := os.Getenv("TEAMUP_PORT")
	if port == "" {
		port = "8080"
	}
	return probeReady("http://localhost:" + port + "/healthz/ready")
}

// probeReady reports whether the server at url says it is ready.
func probeReady(url string) error {
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("not ready: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("not ready: %s answered %d", url, resp.StatusCode)
	}
	return nil
}
