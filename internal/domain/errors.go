package domain

import "errors"

// Sentinel errors returned by the domain. Callers match them with errors.Is.
var (
	ErrTimeTravel     = errors.New("domain: clock cannot move backwards")
	ErrDuplicateNode  = errors.New("domain: duplicate node id")
	ErrUnknownNode    = errors.New("domain: unknown node id")
	ErrInvalidPlan    = errors.New("domain: invalid fault plan")
	ErrParse          = errors.New("domain: parse error")
	ErrInvalidHistory = errors.New("domain: invalid history")
	ErrInvalidConfig  = errors.New("domain: invalid configuration")
)
