package controlplane

import (
	"encoding/json"
	"testing"

	jobmodel "mini-modal/internal/job"
	postgresdb "mini-modal/internal/store/postgres/sqlc"
)

func TestSameSubmission(t *testing.T) {
	params, err := createJobParams(jobmodel.Spec{ID: "job-1", FunctionVersionID: "version-1", Priority: 2, Input: json.RawMessage(`{"args":[1],"kwargs":{}}`)})
	if err != nil {
		t.Fatal(err)
	}
	existing := postgresdb.Job{ID: params.ID, FunctionVersionID: params.FunctionVersionID, Priority: params.Priority, InputJson: json.RawMessage(`{"kwargs":{}, "args":[1]}`), Status: "running", Attempt: 2}
	if !sameSubmission(existing, params) {
		t.Fatal("equivalent JSON inputs should be idempotent")
	}
	changes := []postgresdb.Job{
		{ID: "job-2", FunctionVersionID: existing.FunctionVersionID, Priority: existing.Priority, InputJson: existing.InputJson},
		{ID: existing.ID, FunctionVersionID: "version-2", Priority: existing.Priority, InputJson: existing.InputJson},
		{ID: existing.ID, FunctionVersionID: existing.FunctionVersionID, Priority: 3, InputJson: existing.InputJson},
		{ID: existing.ID, FunctionVersionID: existing.FunctionVersionID, Priority: existing.Priority, InputJson: json.RawMessage(`{"args":[2],"kwargs":{}}`)},
	}
	for _, changed := range changes {
		if sameSubmission(changed, params) {
			t.Fatalf("different submission accepted: %+v", changed)
		}
	}
}
