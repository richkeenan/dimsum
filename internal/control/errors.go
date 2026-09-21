package control

import "errors"

// Adapters wrap these classifications while retaining an actionable message.
var BadRequest = errors.New("invalid request")
var NotFound = errors.New("resource not found")
var Unavailable = ErrUnavailable
