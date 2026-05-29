package browserbridge

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// tokenBytes is the number of random bytes used to derive the bearer token.
// 32 bytes -> 64 hex chars. Comfortably above brute-force range; matches
// the entropy of common API keys (e.g. AWS access keys are 20 bytes).
const tokenBytes = 32

// LoadOrCreateToken returns the bearer token at s.Token, generating and
// persisting a new one if the file is absent. The file is always written
// with mode 0600.
//
// Tokens are intentionally not rotated automatically: rotation would require
// the daemon to update the in-container mount, which is read-only. Users who
// want to invalidate the current token can `toolbox browser-bridge uninstall`
// followed by `install`, which regenerates fresh state.
func LoadOrCreateToken(s HostState) (string, error) {
	if tok, err := loadToken(s.Token); err == nil {
		return tok, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	return generateAndWriteToken(s.Token)
}

// LoadToken returns the token file's contents. Returns fs.ErrNotExist when
// the file is missing — callers can decide whether to bootstrap.
func LoadToken(s HostState) (string, error) {
	return loadToken(s.Token)
}

func loadToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("token file %s is empty", path)
	}
	return tok, nil
}

func generateAndWriteToken(path string) (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	tok := hex.EncodeToString(buf)
	if err := fsx.AtomicWriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write token file %s: %w", path, err)
	}
	return tok, nil
}
