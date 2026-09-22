package transport

import (
	"github.com/richkeenan/dimsum/internal/policy"
	"time"
)

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
	// Response borrows the final fitted reply for the observer callback only.
	Response []byte
	// Rule is borrowed from the captured immutable generation for this callback
	// only. Detailed consumers must copy bounded fields before returning.
	Rule        policy.Rule
	Alias       [255]byte
	AliasLength uint8
}

// Observer runs synchronously after fitting the final transport reply. It must
// copy any needed request data, never block on I/O, and not retain borrowed wire.
// Request is nil for slot-admission rejection before parsing.
type Observer func(*Request, Result)
