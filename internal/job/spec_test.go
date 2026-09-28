package job

import (
	"encoding/json"
	"testing"
)

func TestSpecValidation(t *testing.T) {
	valid := Spec{ID: "job-1", FunctionVersionID: "version-1", Input: json.RawMessage(`{"args":[1],"kwargs":{"name":"Ada"}}`)}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid spec: %v", err)
	}
	tests := []Spec{
		{FunctionVersionID: "version-1"},
		{ID: "job-1"},
		{ID: "job-1", FunctionVersionID: "version-1", Input: json.RawMessage(`[]`)},
		{ID: "job-1", FunctionVersionID: "version-1", Input: json.RawMessage(`{"args":1}`)},
		{ID: "job-1", FunctionVersionID: "version-1", Input: json.RawMessage(`{"kwargs":[]}`)},
		{ID: "job-1", FunctionVersionID: "version-1", Input: json.RawMessage(`{"unexpected":true}`)},
	}
	for _, spec := range tests {
		if err := spec.Validate(); err == nil {
			t.Fatalf("invalid spec accepted: %+v", spec)
		}
	}
}
