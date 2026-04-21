package envelope

import (
	"errors"
	"fmt"
)

// ErrInvalid wraps validation failures against the envelope contract.
var ErrInvalid = errors.New("envelope invalid")

// Validate performs structural validation of required envelope fields. It is
// deliberately a Go-side check for now; the authoritative schema lives at
// schemas/envelope.v1.json and will drive full JSON Schema validation once
// the validator dependency lands.
func Validate(e *Envelope) error {
	if e == nil {
		return fmt.Errorf("%w: nil envelope", ErrInvalid)
	}
	if e.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: schema_version=%q, want %q", ErrInvalid, e.SchemaVersion, SchemaVersion)
	}
	if e.ID == "" {
		return fmt.Errorf("%w: id required", ErrInvalid)
	}
	if e.ProjectID == "" {
		return fmt.Errorf("%w: project_id required", ErrInvalid)
	}
	if e.TaskType == "" {
		return fmt.Errorf("%w: task_type required", ErrInvalid)
	}
	if e.Backend == "" {
		return fmt.Errorf("%w: backend required", ErrInvalid)
	}
	return nil
}
