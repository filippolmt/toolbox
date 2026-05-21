package browserbridge

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateURL_Accepts(t *testing.T) {
	cases := []string{
		"http://example.com",
		"https://example.com/path?q=1#frag",
		"HTTPS://Example.com",
	}
	for _, in := range cases {
		got, err := ValidateURL(in)
		if err != nil {
			t.Errorf("ValidateURL(%q) err = %v", in, err)
		}
		if !strings.HasPrefix(got, "http") {
			t.Errorf("ValidateURL(%q) = %q, lost scheme", in, got)
		}
	}
}

func TestValidateURL_Rejects(t *testing.T) {
	cases := []struct {
		in      string
		wantErr error
	}{
		{"", ErrURLEmpty},
		{"file:///etc/passwd", ErrSchemeNotAllowed},
		{"javascript:alert(1)", ErrSchemeNotAllowed},
		{"data:text/html,<x>", ErrSchemeNotAllowed},
		{"chrome://settings", ErrSchemeNotAllowed},
		{"mailto:x@y.z", ErrSchemeNotAllowed},
		{"https://", ErrURLMalformed},
		{strings.Repeat("a", MaxURLLen+1), ErrURLTooLong},
	}
	for _, c := range cases {
		_, err := ValidateURL(c.in)
		if !errors.Is(err, c.wantErr) {
			t.Errorf("ValidateURL(%q) err = %v, want %v", c.in, err, c.wantErr)
		}
	}
}
