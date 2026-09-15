package rag

import (
	"context"
	"time"
)

// SpanRecord is one executed stage of a request. Only stages that actually
// ran are recorded; a failed stage keeps its classified code in metadata.
type SpanRecord struct {
	Name       string
	StartedAt  time.Time
	DurationMS int64
	Metadata   map[string]any
}

// TraceStore persists one complete TraceRecord — the trace row and every
// span — atomically. An error here is a real persistence failure: the caller
// must not report the request as a fully traceable success.
type TraceStore interface {
	Insert(ctx context.Context, record TraceRecord) error
}
