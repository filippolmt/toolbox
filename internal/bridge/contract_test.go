package bridge

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/filippolmt/toolbox/internal/proximo"
)

// shimDir is the on-disk root of the container-side bridge shims, relative to
// this package.
const shimDir = "../build/assets/bin/"

// readShim returns the on-disk body of one container-side shim.
func readShim(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(shimDir + name)
	if err != nil {
		t.Fatalf("read shim %s: %v", name, err)
	}
	return string(raw)
}

// TestBridgeContract_ShimMatchesGo binds the daemon↔shim wire contract: every
// container-side path, route and JSON field the shell shims hardcode must
// equal the Go constant the daemon serves. They live in two languages linked
// only by comments ("Must match BRIDGE_SOCK in bridge-lib.sh"), and a JSON
// struct tag cannot reference a constant, so nothing but a test holds the two
// ends together — a rename on either side otherwise breaks the bridge
// silently. Mirrors the init.d + Tool Catalog bijection tests: drift is a red
// test, not a field report.
func TestBridgeContract_ShimMatchesGo(t *testing.T) {
	// post is the shim-side spelling of a route: the transport call in
	// bridge-lib.sh takes the endpoint as its first argument.
	post := func(route string) string { return "bridge_post " + route }
	// field is the shim-side spelling of a request key — the one form shared
	// by the printf templates and the python3 encoders.
	field := func(name string) string { return `"` + name + `":` }
	// get is the shim-side spelling of a response key read back via python3.
	get := func(name string) string { return `resp.get("` + name + `"` }

	for _, tc := range []struct {
		shim     string
		literals []struct{ name, literal string }
	}{
		{"bridge-lib.sh", []struct{ name, literal string }{
			{"ContainerDir", ContainerDir},
			{"LegacyContainerDir", LegacyContainerDir},
			{"ContainerSocket", ContainerSocket},
			{"tokenFile", tokenFile},
			{"portFile", portFile},
		}},
		{"xdg-open", []struct{ name, literal string }{
			{"RouteOpen", post(RouteOpen)},
			{"FieldURL", field(FieldURL)},
		}},
		{"code", []struct{ name, literal string }{
			{"RouteEdit", post(RouteEdit)},
			{"FieldEditor", field(FieldEditor)},
			{"FieldPath", field(FieldPath)},
		}},
		{"proximo", []struct{ name, literal string }{
			{"RouteProximo", post(RouteProximo)},
			{"FieldCommand", field(FieldCommand)},
			{"FieldArgs", field(FieldArgs)},
			{"FieldHome", field(FieldHome)},
			{"FieldCodexHome", field(FieldCodexHome)},
			// The agent homes travel host CLI → container env → shim → daemon;
			// the env names are the one hop no compiler checks.
			{"HostAgentHomeEnv", HostAgentHomeEnv},
			{"HostCodexHomeEnv", HostCodexHomeEnv},
			{"FieldOutput", get(FieldOutput)},
			{"FieldExit", get(FieldExit)},
			// The Proximo Availability Gate: the shim's pre-POST file test and
			// the Go constant naming the CA bind target are the same path, or
			// the gate silently stops gating.
			{"proximo.CATarget", proximo.CATarget},
		}},
		{"hc-host", []struct{ name, literal string }{
			{"RouteHC", post(RouteHC)},
			{"FieldCommand", field(FieldCommand)},
			{"FieldArgs", field(FieldArgs)},
			{"FieldOutput", get(FieldOutput)},
			{"FieldExit", get(FieldExit)},
		}},
		{"paplay", []struct{ name, literal string }{
			{"RouteSound", post(RouteSound)},
			{"FieldName", field(FieldName)},
			{"FieldData", field(FieldData)},
		}},
		{"git-credential-toolbox", []struct{ name, literal string }{
			{"RouteCredential", post(RouteCredential)},
			{"FieldOp", field(FieldOp)},
			{"FieldInput", field(FieldInput)},
			{"FieldNoPrompt", field(FieldNoPrompt)},
			{"FieldOutput", get(FieldOutput)},
		}},
	} {
		t.Run(tc.shim, func(t *testing.T) {
			shim := readShim(t, tc.shim)
			for _, c := range tc.literals {
				if !strings.Contains(shim, c.literal) {
					t.Errorf("%s: shim %s does not contain %q — Go/shell bridge contract drifted", c.name, tc.shim, c.literal)
				}
			}
		})
	}
}

// TestBridgeContract_ProximoAllowlistMatchesShim pins the second half of the
// proximo gate. The shim rejects anything outside its own `case` arm before
// the daemon is ever reached, so a command added to the daemon's allowlist
// stays unreachable until the shim learns it — and one dropped from the map
// leaves a shim still advertising it. Both directions are drift, so this is a
// set equality, not a containment.
func TestBridgeContract_ProximoAllowlistMatchesShim(t *testing.T) {
	assertShimDispatchMatches(t, "proximo", AllowedProximoCommands())
}

// TestBridgeContract_HCAllowlistMatchesShim is the same set equality for the
// Host Terminal Relay: hc-host's dispatch arm and the daemon's hc verb gate.
func TestBridgeContract_HCAllowlistMatchesShim(t *testing.T) {
	assertShimDispatchMatches(t, "hc-host", AllowedHCCommands())
}

// assertShimDispatchMatches reads the shim's single dispatch arm, the line
// assigning COMMAND:
//
//	up | down | status) COMMAND="$1" ;;
//
// and requires it to name exactly the daemon's allowed verbs.
func assertShimDispatchMatches(t *testing.T, name string, allowed []string) {
	t.Helper()
	shim := readShim(t, name)
	var arm string
	for line := range strings.SplitSeq(shim, "\n") {
		if strings.Contains(line, `COMMAND="$1"`) {
			arm = line
			break
		}
	}
	if arm == "" {
		t.Fatalf(`shim %s has no COMMAND="$1" dispatch arm — the allowlist can no longer be read from it`, name)
	}
	got := map[string]struct{}{}
	for alt := range strings.SplitSeq(strings.Split(arm, ")")[0], "|") {
		got[strings.TrimSpace(alt)] = struct{}{}
	}
	for _, cmd := range allowed {
		if _, ok := got[cmd]; !ok {
			t.Errorf("the daemon allows %q but shim %s rejects it — the command is unreachable from the container", cmd, name)
		}
	}
	for cmd := range got {
		if !slices.Contains(allowed, cmd) {
			t.Errorf("shim %s forwards %q but the daemon allowlist rejects it — the daemon answers 400", name, cmd)
		}
	}
}

// TestBridgeContract_ShimMaxTimeOutlastsTheDaemon: a shim whose curl gives up
// before the daemon's execution budget reports "bridge unreachable" for a
// command still running on the host. Each long-running route's shim waits the
// daemon's budget plus shimMaxTimeMargin, so moving a timeout moves its shim.
func TestBridgeContract_ShimMaxTimeOutlastsTheDaemon(t *testing.T) {
	const shimMaxTimeMargin = 15 * time.Second
	for _, tc := range []struct {
		shim, route string
		budget      time.Duration
	}{
		{"proximo", RouteProximo, proximoTimeout},
		{"hc-host", RouteHC, hcTimeout},
	} {
		want := fmt.Sprintf(`bridge_post %s "${PAYLOAD}" "${BODY_FILE}" %d)`, tc.route, int((tc.budget + shimMaxTimeMargin).Seconds()))
		if shim := readShim(t, tc.shim); !strings.Contains(shim, want) {
			t.Errorf("shim %s lacks %q: its --max-time must be the daemon's budget plus %v", tc.shim, want, shimMaxTimeMargin)
		}
	}
}

// TestBridgeContract_EveryRouteIsInTheSecurityBoundary: docs/bridge.md's
// "Security boundary" is where a reader learns what each route lets a
// container do on the host. A route the daemon serves but that section never
// names is a host capability nobody reviewed in prose. The routes are read off
// contract.go's Route* constants, so a new one is covered without editing
// this test; /healthz grants nothing and is exempt.
func TestBridgeContract_EveryRouteIsInTheSecurityBoundary(t *testing.T) {
	src, err := parser.ParseFile(token.NewFileSet(), "contract.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile("../../docs/bridge.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(doc), "## Security boundary")
	if !ok {
		t.Fatal(`docs/bridge.md has no "## Security boundary" section`)
	}
	section, _, _ = strings.Cut(section, "\n## ")
	var routes int
	for _, decl := range src.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Route") || name.Name == "RouteHealth" {
					continue
				}
				routes++
				route, err := strconv.Unquote(vs.Values[i].(*ast.BasicLit).Value)
				if err != nil {
					t.Fatalf("%s: %v", name.Name, err)
				}
				if !strings.Contains(section, "`"+route+"`") {
					t.Errorf("%s (%s) is not named in docs/bridge.md's Security boundary", name.Name, route)
				}
			}
		}
	}
	if routes == 0 {
		t.Fatal("no Route* constants found in contract.go — the parse no longer sees them")
	}
}

// TestBridgeContract_EditorAllowlistHasShim pins the editor half: bin/code
// infers the editor from its own basename, so an allowlisted editor only
// exists in the container if the image installs a shim under that exact name
// (a COPY for `code`, a symlink for every synonym). Without one the allowlist
// entry is dead weight the daemon would happily launch on the host.
func TestBridgeContract_EditorAllowlistHasShim(t *testing.T) {
	raw, err := os.ReadFile("../build/assets/Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	dockerfile := string(raw)
	for _, editor := range AllowedEditors() {
		if !strings.Contains(dockerfile, "/usr/local/bin/"+editor) {
			t.Errorf("AllowedEditors has %q but the Dockerfile installs no /usr/local/bin/%s shim — the editor is unreachable from the container", editor, editor)
		}
	}
}

// TestBridgeContract_JSONTagsMatchConstants closes the one gap the compiler
// cannot: a struct tag is an uninterpreted string literal, so renaming a Field*
// constant leaves the tag — the thing actually on the wire — untouched and the
// build green. Each body type is listed with its tags in declaration order.
func TestBridgeContract_JSONTagsMatchConstants(t *testing.T) {
	for _, tc := range []struct {
		typ  reflect.Type
		tags []string
	}{
		{reflect.TypeFor[openRequest](), []string{FieldURL}},
		{reflect.TypeFor[editRequest](), []string{FieldEditor, FieldPath}},
		{reflect.TypeFor[proximoRequest](), []string{FieldCommand, FieldArgs, FieldHome, FieldCodexHome}},
		{reflect.TypeFor[credentialRequest](), []string{FieldOp, FieldInput, FieldNoPrompt}},
		{reflect.TypeFor[commandResponse](), []string{FieldExit, FieldOutput}},
		{reflect.TypeFor[soundRequest](), []string{FieldName, FieldData}},
		{reflect.TypeFor[hcRequest](), []string{FieldCommand, FieldArgs}},
	} {
		t.Run(tc.typ.Name(), func(t *testing.T) {
			if got := tc.typ.NumField(); got != len(tc.tags) {
				t.Fatalf("%d fields, want %d — a body field was added or removed without updating the wire contract", got, len(tc.tags))
			}
			for i, want := range tc.tags {
				if got := tc.typ.Field(i).Tag.Get("json"); got != want {
					t.Errorf("field %s: json tag = %q, want %q", tc.typ.Field(i).Name, got, want)
				}
			}
		})
	}
}
