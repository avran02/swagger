package types

// SecurityType defines the authentication scheme for an endpoint.
type SecurityType string

const (
	// Public means no authentication required.
	Public SecurityType = "public"
	// Bearer means JWT/Bearer token authentication required.
	Bearer SecurityType = "bearer"
)

// EndpointMeta holds all metadata for a registered endpoint.
type EndpointMeta struct {
	Path        string
	Method      string
	RequestDTO  any
	ResponseDTO any
	Security    SecurityType
	Tags        []string
	HandlerPtr  uintptr
}
