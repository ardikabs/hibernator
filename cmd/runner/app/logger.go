/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package app

import (
	"context"

	streamclient "github.com/ardikabs/hibernator/internal/streaming/client"
)

// streamingLogSender adapts StreamingClient to logsink.LogSender interface.
type streamingLogSender struct {
	client streamclient.StreamingClient
}

func (s *streamingLogSender) Log(ctx context.Context, level, message string, fields map[string]string) error {
	return s.client.Log(ctx, level, message, fields)
}
