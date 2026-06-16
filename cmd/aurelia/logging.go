package main

import "gopkg.in/natefinch/lumberjack.v2"

// LogWriter returns a size-rotating writer for the daemon log file.
//
// The daemon's slog output is written here instead of relying on launchd's
// StandardErrorPath capture, which holds the file descriptor open and never
// rotates — daemon.log grew to ~300MB. lumberjack rotates by size and keeps a
// bounded number of compressed backups.
func LogWriter(path string) *lumberjack.Logger {
	return &lumberjack.Logger{
		Filename:   path,
		MaxSize:    50, // megabytes before rotation
		MaxBackups: 5,  // rotated files to retain
		MaxAge:     30, // days to retain
		Compress:   true,
	}
}
