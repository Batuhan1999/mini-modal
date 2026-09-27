package controlplane

import (
	"testing"

	jobmodel "mini-modal/internal/job"
	sqlitedb "mini-modal/internal/store/sqlite/sqlc"
)

func TestSameSubmission(t *testing.T) {
	params, err := createJobParams(jobmodel.Spec{
		ID:                  "job-1",
		Priority:            2,
		GPUType:             "A100",
		GPUs:                1,
		CPUs:                4,
		MemoryGB:            8,
		DurationSeconds:     30,
		Image:               "alpine:3.20",
		Command:             "echo",
		Args:                []string{"hello"},
		MaxAttempts:         5,
		RetryBackoffSeconds: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	existing := sqlitedb.Job{
		ID:                  params.ID,
		Priority:            params.Priority,
		GpuType:             params.GpuType,
		Gpus:                params.Gpus,
		Cpus:                params.Cpus,
		MemoryGb:            params.MemoryGb,
		DurationSeconds:     params.DurationSeconds,
		Image:               params.Image,
		Command:             params.Command,
		Args:                params.Args,
		MaxAttempts:         params.MaxAttempts,
		RetryBackoffSeconds: params.RetryBackoffSeconds,
		Status:              "running",
		Attempt:             2,
		NodeID:              "node-a",
		LastError:           "previous attempt failed",
	}
	if !sameSubmission(existing, params) {
		t.Fatal("mutable job state should not affect submission comparison")
	}

	tests := []struct {
		name   string
		change func(*sqlitedb.Job)
	}{
		{"id", func(j *sqlitedb.Job) { j.ID = "job-2" }},
		{"priority", func(j *sqlitedb.Job) { j.Priority++ }},
		{"gpu type", func(j *sqlitedb.Job) { j.GpuType = "H100" }},
		{"gpus", func(j *sqlitedb.Job) { j.Gpus++ }},
		{"cpus", func(j *sqlitedb.Job) { j.Cpus++ }},
		{"memory", func(j *sqlitedb.Job) { j.MemoryGb++ }},
		{"duration", func(j *sqlitedb.Job) { j.DurationSeconds++ }},
		{"image", func(j *sqlitedb.Job) { j.Image = "ubuntu:latest" }},
		{"command", func(j *sqlitedb.Job) { j.Command = "sleep" }},
		{"args", func(j *sqlitedb.Job) { j.Args = `["different"]` }},
		{"max attempts", func(j *sqlitedb.Job) { j.MaxAttempts++ }},
		{"retry backoff", func(j *sqlitedb.Job) { j.RetryBackoffSeconds++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := existing
			test.change(&changed)
			if sameSubmission(changed, params) {
				t.Fatal("different submission was treated as the same job")
			}
		})
	}
}
