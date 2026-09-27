package controlplane

import (
	"context"
	"fmt"
	"time"

	jobmodel "mini-modal/internal/job"
	postgresdb "mini-modal/internal/store/postgres/sqlc"

	"github.com/google/uuid"
)

func recordJobEvent(
	ctx context.Context,
	queries *postgresdb.Queries,
	job postgresdb.Job,
	event jobmodel.HistoryEvent,
	message string,
	createdAt time.Time,
) error {
	if _, err := queries.CreateJobEvent(ctx, postgresdb.CreateJobEventParams{
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
