package types

// SecurityType defines the authentication scheme for an endpoint.
type SecurityType string

const (
	// Public means no authentication required.
	Public SecurityType = "public"
	// Bearer means JWT/Bearer token authentication required.
	Bearer SecurityType = "bearer"
)

// ErrorSpec declares one error response an endpoint can return.
type ErrorSpec struct {
	// Code is the HTTP status code, e.g. 409.
	Code int
	// Description is the human-readable response description. Empty falls
	// back to http.StatusText(Code).
	Description string
	// Schema is the response body DTO. nil falls back to the global
	// Config.ErrorSchema.
	Schema any
}

// EndpointMeta holds all metadata for a registered endpoint.
type EndpointMeta struct {
	Path        string
	Method      string
	Summary     string
	RequestDTO  any
	ResponseDTO any
	Security    SecurityType
	Tags        []string
	Errors      []ErrorSpec
	HandlerPtr  uintptr
}
