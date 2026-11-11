package auth

import (
	"context"
	"errors"
	"testing"
)

func resolver(env map[string]string, onPath bool, existing string, out, stderr string, runErr error) Resolver {
	return Resolver{
		Getenv: func(k string) string { return env[k] },
		LookPath: func(string) (string, error) {
			if onPath {
				return "/bin/gh", nil
			}
			return "", errors.New("not found")
		},
		Stat: func(p string) error {
			if p == existing {
				return nil
			}
			return errors.New("missing")
		},
		Run: func(context.Context, string, ...string) (string, string, error) { return out, stderr, runErr },
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name  string
		r     Resolver
		state State
		token string
	}{
		{"env wins", resolver(map[string]string{"GH_TOKEN": "envtok"}, false, "", "", "", nil), Ready, "envtok"},
		{"gh on PATH", resolver(nil, true, "", "tok\n", "", nil), Ready, "tok"},
		{"gh in Homebrew only", resolver(nil, false, "/opt/homebrew/bin/gh", "tok\n", "", nil), Ready, "tok"},
		{"gh missing", resolver(nil, false, "", "", "", nil), GhNotInstalled, ""},
		{"logged out", resolver(nil, true, "", "", "no oauth token found for github.com\n", errors.New("exit 1")), GhNotLoggedIn, ""},
		{"empty token", resolver(nil, true, "", "\n", "", nil), GhNotLoggedIn, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.r.Resolve(context.Background())
			if got.State != tt.state || got.Token != tt.token {
				t.Errorf("got %+v, want state %v token %q", got, tt.state, tt.token)
			}
		})
	}
}

func TestResolveKeepsGhMessage(t *testing.T) {
	r := resolver(nil, true, "", "", "no oauth token found for github.com\nmore\n", errors.New("exit 1"))
	if got := r.Resolve(context.Background()); got.Detail != "no oauth token found for github.com" {
		t.Errorf("detail %q", got.Detail)
	}
}
