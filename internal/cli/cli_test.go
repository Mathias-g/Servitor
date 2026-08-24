package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := Run([]string{"version"}, &out, &errOut); err != nil {
		t.Fatalf("version: unexpected error: %v", err)
	}
	if !strings.HasPrefix(out.String(), "servitor ") {
		t.Errorf("version output %q does not start with 'servitor '", out.String())
	}
}

func TestUnknownCommandFails(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := Run([]string{"submit", "wf.yml"}, &out, &errOut); err == nil {
		t.Fatal("expected an error for an unimplemented command, got nil")
	}
}

func TestHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := Run([]string{"help"}, &out, &errOut); err != nil {
		t.Fatalf("help: unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "capabilities") {
		t.Errorf("help output should mention capabilities, got %q", out.String())
	}
}
