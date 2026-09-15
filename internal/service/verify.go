package service

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// startupProbe bounds how long install waits for the service to answer. A
// service manager reports "started" the moment it has spawned the process, so
// without this an install that crash-loops still looks like a success.
const (
	startupProbeTimeout  = 15 * time.Second
	startupProbeInterval = 500 * time.Millisecond
)

// verifyRunning polls /healthz until the freshly installed service answers.
//
// It checks the *body*, not just the status code: if something else already
// owns the port — which is how this failure usually presents — the probe would
// otherwise be satisfied by a completely unrelated server.
func verifyRunning(log *slog.Logger, port int) error {
	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/healthz"
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(startupProbeTimeout)

	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = probe(client, url)
		if lastErr == nil {
			log.Info("the service is answering", "url", url)
			return nil
		}
		time.Sleep(startupProbeInterval)
	}

	return fmt.Errorf(
		"the service was installed but is not answering on port %d after %s: %w.\n"+
			"Check whether something else already uses that port, and read the log in the data directory",
		port, startupProbeTimeout, lastErr)
}

func probe(client *http.Client, url string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return fmt.Errorf("read the health response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned %d", resp.StatusCode)
	}

	var health struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &health); err != nil || health.Status == "" {
		return fmt.Errorf("port %s is answering, but not with bokkie's health response — is another program using it?", url)
	}
	return nil
}
