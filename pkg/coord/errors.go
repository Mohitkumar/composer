package coord

import "errors"

// Sentinel errors returned (wrapped) by Coordinator methods. API layers map
// them to status codes with errors.Is; anything else is an internal error.
var (
	// ErrNotFound: the workflow, version or run does not exist (or is deleted).
	ErrNotFound = errors.New("not found")
	// ErrInvalid: the request is malformed or not allowed for this resource.
	ErrInvalid = errors.New("invalid request")
	// ErrConflict: the request conflicts with the resource's current state.
	ErrConflict = errors.New("conflict")
	// ErrRunActive: a retry targets a run that still has tasks in flight.
	ErrRunActive = errors.New("workflow run still has active tasks")
	// ErrRunNotActive: the run already finished, so it cannot accept the request.
	ErrRunNotActive = errors.New("workflow run is not active")
)
