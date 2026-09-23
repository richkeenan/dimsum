package control

// FieldError identifies invalid request input using a path relative to the JSON
// request body. It is shared by CLI, HTTP and MCP rather than transport-specific.
type FieldError struct {
	Path    []string `json:"path"`
	Message string   `json:"message"`
}

func (e *FieldError) Error() string { return e.Message }
func (e *FieldError) Unwrap() error { return BadRequest }
