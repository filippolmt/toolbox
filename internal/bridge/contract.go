package bridge

// The daemon↔shim wire contract. Everything the container-side shell shims
// hardcode lives here as one exported literal, so the Go side has a single
// place to rename and TestBridgeContract_ShimMatchesGo has a single place to
// read. The paths half of the contract (state dir, socket, token/port files)
// lives in paths.go.

// Routes the daemon mounts and the shims POST to. RouteHealth is the only one
// with no shim consumer — it is the unauthenticated liveness probe.
const (
	RouteOpen       = "/open"
	RouteEdit       = "/edit"
	RouteProximo    = "/proximo"
	RouteCredential = "/credential"
	RouteHealth     = "/healthz"
)

// JSON object keys of the request and response bodies. A Go struct tag cannot
// reference a constant, so these are bound to the actual tags by
// TestBridgeContract_JSONTagsMatchConstants rather than by the compiler.
const (
	// Request keys.
	FieldURL     = "url"     // openRequest
	FieldEditor  = "editor"  // editRequest
	FieldPath    = "path"    // editRequest
	FieldCommand = "command" // proximoRequest
	FieldOp      = "op"      // credentialRequest
	FieldInput   = "input"   // credentialRequest

	// Response keys, shared by proximoResponse and credentialResponse.
	FieldExit   = "exit"
	FieldOutput = "output"
)
