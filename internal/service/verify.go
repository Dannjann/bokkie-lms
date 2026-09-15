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
func verifyRunning(log *slog.Logger, port int) error {
	_, err := WaitUntilHealthy(log, port)
	return err
}

// WaitUntilHealthy polls /healthz until the service answers, and reports the
// version that answered.
//
// It checks the *body*, not just the status code: if something else already
// owns the port — which is how this failure usually presents — the probe would
// otherwise be satisfied by a completely unrelated server. Returning the
// version is what lets an update confirm that the process now serving is the
// one it just installed, rather than an old one that never restarted.
func WaitUntilHealthy(log *slog.Logger, port int) (string, error) {
	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/healthz"
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(startupProbeTimeout)

	var lastErr error
	for time.Now().Before(deadline) {
		var health healthBody
		health, lastErr = probe(client, url)
		if lastErr == nil {
			log.Info("the service is answering", "url", url, "version", health.Version)
			return health.Version, nil
		}
		time.Sleep(startupProbeInterval)
	}

	return "", fmt.Errorf(
		"the service is not answering on port %d after %s: %w.\n"+
			"Check whether something else already uses that port, and read the log in the data directory",
		port, startupProbeTimeout, lastErr)
}

// ServingVersion asks the running service which version it is, in one attempt.
//
// This is the honest answer to "what is installed here", and it is not the same
// as the version of the process asking. An update run from the binary on the
// USB stick would otherwise compare the new version against itself and conclude
// there was nothing to do.
func ServingVersion(port int) (string, error) {
	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/healthz"
	health, err := probe(&http.Client{Timeout: 2 * time.Second}, url)
	if err != nil {
		return "", err
	}
	return health.Version, nil
}

type healthBody struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

func probe(client *http.Client, url string) (healthBody, error) {
	var health healthBody

	resp, err := client.Get(url)
	if err != nil {
		return health, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return health, fmt.Errorf("read the health response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return health, fmt.Errorf("health check returned %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, &health); err != nil || health.Status == "" {
		return health, fmt.Errorf("port %s is answering, but not with bokkie's health response — is another program using it?", url)
	}
	return health, nil
}
