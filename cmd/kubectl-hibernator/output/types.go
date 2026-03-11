/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package output

// MessageType represents the semantic meaning of an output message.
type MessageType int

const (
	MessageTypeSuccess MessageType = iota
	MessageTypeWarning
	MessageTypeError
	MessageTypeHint
	MessageTypeInfo
)
