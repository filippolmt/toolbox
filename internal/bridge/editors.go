package bridge

// editorAllowlist is the fixed set of editors /edit may launch. A
// client-supplied name never reaches exec without passing this gate.
var editorAllowlist = map[string]struct{}{
	"code":   {},
	"codium": {},
}

// editorApps maps every allowlisted editor CLI name to its macOS app bundle
// name, used by editor_darwin.go as the `open -a` fallback when the CLI shim
// is not on PATH (the default VS Code install does not add `code` to PATH).
// Kept next to editorAllowlist — untagged, even though only darwin reads it —
// so TestEditorAppsCoversAllowlist can enforce the bijection on every
// platform CI runs on.
var editorApps = map[string]string{
	"code":   "Visual Studio Code",
	"codium": "VSCodium",
}
