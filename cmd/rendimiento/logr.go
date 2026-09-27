package main

import (
	"log/slog"

	"github.com/go-logr/logr"
)

func toLogr(l *slog.Logger) logr.Logger { return logr.FromSlogHandler(l.Handler()) }
