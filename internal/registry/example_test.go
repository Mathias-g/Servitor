package registry_test

import (
	"testing"

	"github.com/Mathias-g/Servitor/internal/registry"
	_ "github.com/Mathias-g/Servitor/internal/registry/mechanisms"
)

func TestNodeExampleFromExamples(t *testing.T) {
	// http has examples on url/method/timeout; the example must carry those
	// sample values so it cannot drift from the schema.
	ex := registry.LookupNode("http").NodeExample()
	if ex["url"] != "https://api.example.com/things" {
		t.Fatalf("example url = %v, want the schema example", ex["url"])
	}
	if ex["method"] != "GET" {
		t.Fatalf("example method = %v, want GET", ex["method"])
	}
	if ex["timeout"] != 30 {
		t.Fatalf("example timeout = %v, want 30", ex["timeout"])
	}
	if ex["type"] != "http" {
		t.Fatalf("example type = %v, want http", ex["type"])
	}
}

func TestTriggerExample(t *testing.T) {
	ex := registry.LookupTrigger("cron").TriggerExample()
	if ex["schedule"] != "0 * * * *" {
		t.Fatalf("example schedule = %v, want the schema example", ex["schedule"])
	}
	if ex["type"] != "cron" {
		t.Fatalf("example type = %v, want cron", ex["type"])
	}
}

func TestExampleCoversRequiredFields(t *testing.T) {
	// Every required field must appear in the derived example, so the example
	// validates against the schema.
	for _, st := range registry.Nodes() {
		ex := st.NodeExample()
		for name, f := range st.Fields {
			if f.Required {
				if _, ok := ex[name]; !ok {
					t.Fatalf("node %s: required field %q missing from example", st.Name, name)
				}
			}
		}
	}
	for _, tt := range registry.TriggerTypes() {
		ex := tt.TriggerExample()
		for name, f := range tt.Fields {
			if f.Required {
				if _, ok := ex[name]; !ok {
					t.Fatalf("trigger %s: required field %q missing from example", tt.Name, name)
				}
			}
		}
	}
}
