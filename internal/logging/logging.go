// Package logging sets up the process-wide slog logger.
//
// These logs are read by whoever is standing at the machine — or emailed as a
// file when something has gone wrong at the station — not shipped to a log
// aggregator. Two consequences shape this package:
//
//   - The format is human-readable text, one day per file, so "what happened on
//     Tuesday" is a file rather than a query.
//   - **Never log PII.** Customer names, addresses and amounts do not belong in
//     a file that gets copied onto a USB stick for debugging. Log identifiers —
//     customer ID, voucher ref, user ID — and look the rest up in the database.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Options configures the logger. The zero value is not useful; callers build
// this from config.
type Options struct {
	// Dir is the directory daily log files are written to.
	Dir string
	// Retention is how many daily files to keep, including today's.
	Retention int
	// Level is the minimum level written.
	Level slog.Level
	// Console additionally writes to stderr, for `bokkie run` in a terminal.
	// Under a service manager stderr usually goes nowhere, which is exactly why
	// the file is the primary destination rather than the fallback.
	Console bool
}

// ParseLevel maps a config string onto a slog level. Unknown values are an
// error rather than a silent default: a typo'd level that quietly logs nothing
// is discovered at the worst possible moment.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unknown log level %q (want debug, info, warn or error)", s)
	}
}

// New opens the log file and returns a logger plus a closer the caller must run
// on shutdown. The returned logger is not installed as the slog default; the
// caller does that, so tests can hold a logger of their own.
func New(opts Options) (*slog.Logger, io.Closer, error) {
	w, err := newDailyWriter(opts.Dir, opts.Retention)
	if err != nil {
		return nil, nil, err
	}

	var dst io.Writer = w
	if opts.Console {
		dst = io.MultiWriter(w, os.Stderr)
	}

	handler := slog.NewTextHandler(dst, &slog.HandlerOptions{Level: opts.Level})
	return slog.New(handler), w, nil
}
