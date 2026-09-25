package controlplane

import (
	"context"
	"fmt"
	"time"

	jobmodel "mini-modal/internal/job"
	sqlitedb "mini-modal/internal/store/sqlite/sqlc"

	"github.com/google/uuid"
)

func recordJobEvent(
	ctx context.Context,
	queries *sqlitedb.Queries,
	job sqlitedb.Job,
	event jobmodel.HistoryEvent,
	message string,
	createdAt time.Time,
) error {
	if _, err := queries.CreateJobEvent(ctx, sqlitedb.CreateJobEventParams{
		ID:        uuid.NewString(),
		JobID:     job.ID,
		Attempt:   job.Attempt,
		Event:     string(event),
		NodeID:    job.NodeID,
		Message:   message,
		CreatedAt: createdAt.UnixMilli(),
	}); err != nil {
		return fmt.Errorf("record %s event for job %q: %w", event, job.ID, err)
	}
	return nil
}
