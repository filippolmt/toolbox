package browserbridge

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateURL_Accepts(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"http://example.com", "http://example.com"},
		{"https://example.com/path?q=1#frag", "https://example.com/path?q=1#frag"},
		{"HTTPS://Example.com", "https://Example.com"},
	}
	for _, c := range cases {
		got, err := ValidateURL(c.in)
		if err != nil {
			t.Errorf("ValidateURL(%q) err = %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ValidateURL(%q) = %q, want %q", c.in, got, c.want)
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
