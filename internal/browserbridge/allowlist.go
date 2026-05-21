package browserbridge

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// MaxURLLen caps the size of an incoming URL. 8 KiB is well above any
// real-world OAuth redirect (typically <2 KiB) and below the point where
// shells refuse to forward the value, while still rejecting attempts to
// flood the daemon log or the host's `open` argv.
const MaxURLLen = 8192

// allowedSchemes is the closed set of URL schemes the daemon will pass to
// the host's `open` / `xdg-open`. Every other scheme is rejected.
//
// Why so narrow:
//   - file:// would let a malicious in-container process exfiltrate or
//     trigger reads against arbitrary host paths via the user's default
//     handler (e.g. opening ~/.ssh/id_rsa in TextEdit/gedit).
//   - javascript:, data:, vbscript: are classic XSS-style sinks that the
//     OS handler may pass to a running browser instance.
//   - chrome://, about:, mailto: and other custom schemes have wildly
//     different semantics across browsers — staying with the two web
//     schemes keeps the threat model auditable.
var allowedSchemes = map[string]struct{}{
	"http":  {},
	"https": {},
}

// ErrURLEmpty is returned when ValidateURL is called with an empty string.
var ErrURLEmpty = errors.New("url is empty")

// ErrURLTooLong is returned when ValidateURL receives a URL longer than MaxURLLen.
var ErrURLTooLong = errors.New("url exceeds maximum length")

// ErrSchemeNotAllowed is returned when ValidateURL sees a scheme outside allowedSchemes.
var ErrSchemeNotAllowed = errors.New("url scheme is not allowed")

// ErrURLMalformed is returned when ValidateURL cannot parse the input.
var ErrURLMalformed = errors.New("url is malformed")

// ValidateURL applies the allowlist + length cap to the input and returns
// the normalised URL string (scheme lower-cased, fragment preserved) on
// success. Returns one of the sentinel errors above on failure.
func ValidateURL(raw string) (string, error) {
	if raw == "" {
		return "", ErrURLEmpty
	}
	if len(raw) > MaxURLLen {
		return "", fmt.Errorf("%w: %d > %d", ErrURLTooLong, len(raw), MaxURLLen)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrURLMalformed, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if _, ok := allowedSchemes[scheme]; !ok {
		return "", fmt.Errorf("%w: %q", ErrSchemeNotAllowed, scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("%w: missing host", ErrURLMalformed)
	}
	u.Scheme = scheme
	return u.String(), nil
}
