package controlplane

import (
	"errors"

	postgresdb "mini-modal/internal/store/postgres/sqlc"
)

var ErrSubmissionConflict = errors.New("submission conflict")

func sameSubmission(existing postgresdb.Job, requested postgresdb.CreateJobParams) bool {
	return existing.ID == requested.ID &&
		existing.Priority == requested.Priority &&
		existing.GpuType == requested.GpuType &&
		existing.Gpus == requested.Gpus &&
		existing.Cpus == requested.Cpus &&
		existing.MemoryGb == requested.MemoryGb &&
		existing.DurationSeconds == requested.DurationSeconds &&
		existing.Image == requested.Image &&
		existing.Command == requested.Command &&
		existing.Args == requested.Args &&
		existing.MaxAttempts == requested.MaxAttempts &&
		existing.RetryBackoffSeconds == requested.RetryBackoffSeconds
}
