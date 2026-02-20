package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/fsnotify/fsnotify"
)

const watcherDebounce = 500 * time.Millisecond

// StartWatcher watches the spec directory for changes and triggers Reload on modifications.
// It blocks until the context is cancelled.
func (d *Daemon) StartWatcher(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	if err := watcher.Add(d.specDir); err != nil {
		return err
	}

	d.logger.Info("watching spec directory for changes", "dir", d.specDir)

	var debounceTimer *time.Timer

	for {
		select {
		case <-ctx.Done():
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			d.logger.Debug("spec file changed", "file", event.Name, "op", event.Op)

			// Debounce: reset timer on each event
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(watcherDebounce, func() {
				d.logger.Info("reloading specs after file change")
				result, err := d.Reload(ctx)
				if err != nil {
					d.logger.Error("auto-reload failed", "error", err)
					return
				}
				if len(result.Added) > 0 || len(result.Removed) > 0 || len(result.Restarted) > 0 {
					d.logger.Info("auto-reload complete",
						"added", result.Added,
						"removed", result.Removed,
						"restarted", result.Restarted)
				} else {
					d.logger.Debug("auto-reload: no changes detected")
				}
			})

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			slog.Error("file watcher error", "error", err)
		}
	}
}
