package controlplane

import (
	"testing"

	jobmodel "mini-modal/internal/job"
	postgresdb "mini-modal/internal/store/postgres/sqlc"
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

	existing := postgresdb.Job{
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
		change func(*postgresdb.Job)
	}{
		{"id", func(j *postgresdb.Job) { j.ID = "job-2" }},
		{"priority", func(j *postgresdb.Job) { j.Priority++ }},
		{"gpu type", func(j *postgresdb.Job) { j.GpuType = "H100" }},
		{"gpus", func(j *postgresdb.Job) { j.Gpus++ }},
		{"cpus", func(j *postgresdb.Job) { j.Cpus++ }},
		{"memory", func(j *postgresdb.Job) { j.MemoryGb++ }},
		{"duration", func(j *postgresdb.Job) { j.DurationSeconds++ }},
		{"image", func(j *postgresdb.Job) { j.Image = "ubuntu:latest" }},
		{"command", func(j *postgresdb.Job) { j.Command = "sleep" }},
		{"args", func(j *postgresdb.Job) { j.Args = `["different"]` }},
		{"max attempts", func(j *postgresdb.Job) { j.MaxAttempts++ }},
		{"retry backoff", func(j *postgresdb.Job) { j.RetryBackoffSeconds++ }},
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
