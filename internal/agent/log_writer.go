package agent

import (
	"context"
	"io"
	"sync"
	"time"

	agentv1 "mini-modal/gen/agent/v1"
	"mini-modal/internal/domain"
)

// logSequence is shared by the stdout and stderr writers for one assignment.
// The mutex keeps sequence allocation and channel delivery in the same order.
type logSequence struct {
	mu   sync.Mutex
	next int64
}

type logWriter struct {
	ctx        context.Context
	assignment domain.Assignment
	source     agentv1.LogSource
	chunks     chan<- domain.LogStream
	sequence   *logSequence
	uploadDone <-chan struct{}
}

func newLogWriter(
	ctx context.Context,
	assignment domain.Assignment,
	source agentv1.LogSource,
	chunks chan<- domain.LogStream,
	sequence *logSequence,
	uploadDone <-chan struct{},
) *logWriter {
	return &logWriter{
		ctx:        ctx,
		assignment: assignment,
		source:     source,
		chunks:     chunks,
		sequence:   sequence,
		uploadDone: uploadDone,
	}
}

func (w *logWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}

	// Command output buffers may be reused after Write returns, so each chunk
	// must own a copy of its bytes.
	chunkData := append([]byte(nil), data...)

	w.sequence.mu.Lock()
	defer w.sequence.mu.Unlock()

	chunk := domain.LogStream{
		JobID:     w.assignment.JobID,
		Attempt:   w.assignment.Attempt,
		Sequence:  w.sequence.next,
		Source:    w.source,
		Data:      chunkData,
		CreatedAt: time.Now().UTC(),
	}

	select {
	case w.chunks <- chunk:
		w.sequence.next++
		return len(data), nil

	case <-w.uploadDone:
		// Log transport failure should not block or fail the user process.
		return len(data), nil

	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	}
}

var _ io.Writer = (*logWriter)(nil)
