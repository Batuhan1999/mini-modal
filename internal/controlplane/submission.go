package controlplane

import (
	"bytes"
	"encoding/json"
	"errors"

	postgresdb "mini-modal/internal/store/postgres/sqlc"
)

var ErrSubmissionConflict = errors.New("submission conflict")

func sameSubmission(existing postgresdb.Job, requested postgresdb.CreateJobParams) bool {
	leftJSON, leftErr := canonicalJSON(existing.InputJson)
	rightJSON, rightErr := canonicalJSON(requested.InputJson)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return existing.ID == requested.ID &&
		existing.FunctionVersionID == requested.FunctionVersionID &&
		existing.Priority == requested.Priority &&
		string(leftJSON) == string(rightJSON)
}

func canonicalJSON(raw json.RawMessage) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
