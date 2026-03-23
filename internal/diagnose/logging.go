package diagnose

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// SetupLogging creates a session log file and configures slog to write to it.
// Logs are written to ~/.aurelia/logs/diagnose/<timestamp>.log as JSON.
// Returns a cleanup function that closes the file.
func SetupLogging() (func(), error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("finding home dir: %w", err)
	}

	dir := filepath.Join(home, ".aurelia", "logs", "diagnose")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}

	name := time.Now().Format("2006-01-02T15-04-05") + ".log"
	path := filepath.Join(dir, name)

	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create log file: %w", err)
	}

	handler := slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(handler))

	slog.Info("diagnose session started", "log_file", path)

	return func() {
		slog.Info("diagnose session ended")
		f.Close()
	}, nil
}
