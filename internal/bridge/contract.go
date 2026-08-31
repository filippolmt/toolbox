package bridge

import (
	"maps"
	"slices"
)

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
	FieldArgs    = "args"    // proximoRequest
	// The calling session's agent homes, as HOST paths — see proximoAgentHome.
	FieldHome      = "home"       // proximoRequest
	FieldCodexHome = "codex_home" // proximoRequest
	FieldOp        = "op"         // credentialRequest
	FieldInput     = "input"      // credentialRequest

	// Container env carrying the two agent homes the proximo shim forwards as
	// FieldHome / FieldCodexHome. Emitted host-side by sessionplan (the only
	// stage that knows a session's mount plan), read by the shim.
	HostAgentHomeEnv = "TOOLBOX_HOST_AGENT_HOME"
	HostCodexHomeEnv = "TOOLBOX_HOST_CODEX_HOME"

	// Response keys, shared by proximoResponse and credentialResponse.
	FieldExit   = "exit"
	FieldOutput = "output"
)

// AllowedEditors and AllowedProximoCommands are the contract's third part,
// after the paths and the routes/fields: the verbs the container side may
// send at all. They return a
// sorted copy rather than the gate itself — docs/bridge.md documents both
// sets as fixed ("a client-supplied name never reaches exec without passing
// this gate"), and an exported map would let any caller widen a security
// boundary by assignment. The maps stay unexported next to the code that
// gates on them (editors.go, proximo.go).
func AllowedEditors() []string { return slices.Sorted(maps.Keys(editorAllowlist)) }

func AllowedProximoCommands() []string { return slices.Sorted(maps.Keys(proximoAllowlist)) }
