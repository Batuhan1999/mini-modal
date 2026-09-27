package job

import (
	"fmt"
	"strings"
)

type Spec struct {
	ID                  string
	FunctionVersionID   string
	Priority            int64
	GPUType             string
	GPUs                int64
	CPUs                int64
	MemoryGB            int64
	DurationSeconds     int64
	Image               string
	Command             string
	Args                []string
	MaxAttempts         int64
	RetryBackoffSeconds int64
}

func (s Spec) Validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("job ID is required")
	}
	if s.CPUs <= 0 {
		return fmt.Errorf("job %q must request at least one CPU", s.ID)
	}
	if s.MemoryGB <= 0 {
		return fmt.Errorf("job %q must request positive memory", s.ID)
	}
	if s.GPUs < 0 {
		return fmt.Errorf("job %q cannot request negative GPUs", s.ID)
	}
	if s.DurationSeconds < 0 {
		return fmt.Errorf("job %q cannot have a negative duration", s.ID)
	}
	if s.MaxAttempts < 0 {
		return fmt.Errorf("job %q cannot have negative max attempts", s.ID)
	}
	if s.RetryBackoffSeconds < 0 {
		return fmt.Errorf("job %q cannot have a negative retry backoff", s.ID)
	}
	if strings.TrimSpace(s.Image) == "" {
		return fmt.Errorf("job %q requires an image", s.ID)
	}
	if strings.TrimSpace(s.Command) == "" {
		return fmt.Errorf("job %q requires a command", s.ID)
	}
	return nil
}
