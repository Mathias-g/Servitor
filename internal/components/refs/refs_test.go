package refs

import (
	"strings"
	"testing"
)

func TestResolveHeaders(t *testing.T) {
	env := []string{"SEARCH_TOKEN=abc", "PATH=/usr/bin"}
	res, err := ResolveHeaders(map[string]string{
		"Authorization": "Bearer $SEARCH_TOKEN",
		"X-Static":      "value",
	}, env)
	if err != nil {
		t.Fatalf("ResolveHeaders: %v", err)
	}
	if res["Authorization"] != "Bearer abc" {
		t.Fatalf("Authorization = %q, want Bearer abc", res["Authorization"])
	}
	if res["X-Static"] != "value" {
		t.Fatalf("X-Static = %q, want value", res["X-Static"])
	}
}

func TestResolveHeadersMissingSecretFails(t *testing.T) {
	_, err := ResolveHeaders(map[string]string{"Authorization": "Bearer $NOPE"}, []string{"OTHER=x"})
	if err == nil || !strings.Contains(err.Error(), "NOPE") {
		t.Fatalf("ResolveHeaders error = %v, want a missing-secret error", err)
	}
}

func TestReferencedSecrets(t *testing.T) {
	names := ReferencedSecrets(map[string]string{
		"Authorization": "Bearer $SEARCH_TOKEN",
		"X":             "$OTHER",
	})
	if len(names) != 2 || names[0] != "SEARCH_TOKEN" || names[1] != "OTHER" {
		t.Fatalf("names = %v, want [SEARCH_TOKEN OTHER]", names)
	}
}

func TestSubstitute(t *testing.T) {
	vals := map[string]string{"TOKEN": "abc"}
	cases := map[string]string{
		"Bearer $TOKEN":  "Bearer abc",
		"$TOKEN":         "abc",
		"$TOKEN-$TOKEN":  "abc-abc",
		"plain":          "plain",
		"$":              "$",
		"pre $TOKEN suf": "pre abc suf",
	}
	for in, want := range cases {
		got, err := Substitute(in, vals)
		if err != nil {
			t.Fatalf("Substitute(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("Substitute(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSubstituteMissingFails(t *testing.T) {
	if _, err := Substitute("$NOPE", map[string]string{}); err == nil || !strings.Contains(err.Error(), "NOPE") {
		t.Fatalf("Substitute error = %v, want a missing-secret error", err)
	}
}
