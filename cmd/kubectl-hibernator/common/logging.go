/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package common

import (
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewLogger creates a logr.Logger backed by zap for console output.
// This logger is used for structured logging throughout the CLI.
// The logger is configured for console output with appropriate formatting.
func NewLogger() logr.Logger {
	// Configure zap with console-friendly output
	config := zap.Config{
		Level:       zap.NewAtomicLevel(),
		Development: false,
		Encoding:    "console",
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:       "", // Don't include timestamps in console output
			LevelKey:      "level",
			NameKey:       "logger",
			CallerKey:     zapcore.OmitKey,
			MessageKey:    "msg",
			StacktraceKey: zapcore.OmitKey,
			LineEnding:    zapcore.DefaultLineEnding,
			EncodeLevel: func(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
				// Don't encode level in console output for messages
				// (we handle this semantically in Output handler)
			},
			EncodeDuration: zapcore.StringDurationEncoder,
		},
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
	}

	zapLogger, err := config.Build()
	if err != nil {
		panic(err)
	}

	return zapr.NewLogger(zapLogger)
}
