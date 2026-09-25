package job

import "testing"

func TestSpecValidation(t *testing.T) {
	valid := Spec{
		ID: "job-1", CPUs: 1, MemoryGB: 1,
		Image: "alpine:3.20", Command: "echo", Args: []string{"hello"},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid spec: %v", err)
	}

	tests := []struct {
		name string
		spec Spec
	}{
		{name: "missing ID", spec: Spec{CPUs: 1, MemoryGB: 1, Image: "image", Command: "echo"}},
		{name: "zero CPUs", spec: Spec{ID: "job-1", MemoryGB: 1, Image: "image", Command: "echo"}},
		{name: "zero memory", spec: Spec{ID: "job-1", CPUs: 1, Image: "image", Command: "echo"}},
		{name: "negative GPUs", spec: Spec{ID: "job-1", CPUs: 1, MemoryGB: 1, GPUs: -1, Image: "image", Command: "echo"}},
		{name: "negative duration", spec: Spec{ID: "job-1", CPUs: 1, MemoryGB: 1, DurationSeconds: -1, Image: "image", Command: "echo"}},
		{name: "negative max attempts", spec: Spec{ID: "job-1", CPUs: 1, MemoryGB: 1, MaxAttempts: -1, Image: "image", Command: "echo"}},
		{name: "negative retry backoff", spec: Spec{ID: "job-1", CPUs: 1, MemoryGB: 1, RetryBackoffSeconds: -1, Image: "image", Command: "echo"}},
		{name: "missing image", spec: Spec{ID: "job-1", CPUs: 1, MemoryGB: 1, Command: "echo"}},
		{name: "missing command", spec: Spec{ID: "job-1", CPUs: 1, MemoryGB: 1, Image: "image"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.spec.Validate(); err == nil {
				t.Fatal("invalid spec was accepted")
			}
		})
	}
}
