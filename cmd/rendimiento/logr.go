package main

import (
	"log/slog"

	"github.com/go-logr/logr"
)

// toLogr hands controller-runtime the platform's logger, so its messages
// (and warnings, on the Problems page) go the same way.
func toLogr(l *slog.Logger) logr.Logger { return logr.FromSlogHandler(l.Handler()) }
