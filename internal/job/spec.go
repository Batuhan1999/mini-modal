package job

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type Spec struct {
	ID                string
	FunctionVersionID string
	Priority          int64
	Input             json.RawMessage
}

func (s Spec) Validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("job ID is required")
	}
	if strings.TrimSpace(s.FunctionVersionID) == "" {
		return fmt.Errorf("function version ID is required")
	}
	if len(s.Input) > 0 {
		rawInput := bytes.TrimSpace(s.Input)
		if len(rawInput) == 0 || rawInput[0] != '{' || !json.Valid(rawInput) {
			return fmt.Errorf("job %q input must be a JSON object", s.ID)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(rawInput, &fields); err != nil {
			return fmt.Errorf("job %q input must be a JSON object: %w", s.ID, err)
		}
		for key := range fields {
			if key != "args" && key != "kwargs" {
				return fmt.Errorf("job %q input has unknown field %q", s.ID, key)
			}
		}
		if raw, ok := fields["args"]; ok {
			var args []json.RawMessage
			if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '[' || json.Unmarshal(trimmed, &args) != nil {
				return fmt.Errorf("job %q input.args must be an array", s.ID)
			}
		}
		if raw, ok := fields["kwargs"]; ok {
			var kwargs map[string]json.RawMessage
			if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' || json.Unmarshal(trimmed, &kwargs) != nil {
				return fmt.Errorf("job %q input.kwargs must be an object", s.ID)
			}
		}
	}
	return nil
}
