package transport

import "time"

// Outcome is mutually exclusive; attributes such as coalescing do not create
// additional client requests. Invalid packets are not admitted client queries.
type Outcome uint16

const (
	ResolutionError Outcome = iota
	LocalAnswer
	PolicyBlock
	FreshAnswer
	StaleAnswer
	ForwardedAnswer
	AdmissionRejected
)

type Result struct {
	Outcome                                        Outcome
	Admitted                                       bool
	Generation                                     uint64
	RuleNumber                                     uint32
	UpstreamID                                     uint32
	Coalesced, Fallback, Truncated, ResponsePolicy bool
	Arrival                                        time.Time
	Elapsed                                        time.Duration
	RCode                                          uint16
}

// Observer runs synchronously after fitting the final transport reply. It must
// copy any needed request data, never block on I/O, and not retain borrowed wire.
// Request is nil for slot-admission rejection before parsing.
type Observer func(*Request, Result)
