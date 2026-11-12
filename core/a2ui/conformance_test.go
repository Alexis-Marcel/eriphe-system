package a2ui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The conformance cases of the A2UI repository (specification/v1_0/test,
// Apache-2.0, testdata/conformance/NOTICE), run against this package as
// their runner (run_tests.py) runs them against ajv: each suite names the
// schema it targets and, at times, the catalog to put in place of
// catalog.json (the basic catalog otherwise).

type suite struct {
	Schema  string `json:"schema"`
	Catalog string `json:"catalog"`
	Tests   []struct {
		Description string          `json:"description"`
		Valid       *bool           `json:"valid"`
		Data        json.RawMessage `json:"data"`
	} `json:"tests"`
}

func loadTestCatalog(t *testing.T, name string) *Catalog {
	t.Helper()
	if name == "" {
		name = "basic_catalog.json"
	}
	raw, err := os.ReadFile(filepath.Join("testdata/conformance", name))
	if err != nil {
		t.Fatal(err)
	}
	c, err := LoadCatalog(raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return c
}

// schemaValid validates data against one of the protocol's schemas, the
// way the conformance runner does: the schema alone, no tree checks.
func schemaValid(t *testing.T, c *Catalog, schema string, data []byte) error {
	t.Helper()
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	switch schema {
	case "", "agent_to_renderer.json":
		return c.message.Validate(v)
	case "renderer_to_agent.json":
		return c.event.Validate(v)
	case "catalog_definition.json":
		def, err := catalogDefinition()
		if err != nil {
			t.Fatal(err)
		}
		return def.Validate(v)
	}
	comp := newCompiler()
	if err := addSpec(comp); err != nil {
		t.Fatal(err)
	}
	sch, err := comp.Compile(specBase + schema)
	if err != nil {
		t.Fatal(err)
	}
	return sch.Validate(v)
}

func TestConformanceSuites(t *testing.T) {
	files, _ := filepath.Glob("testdata/conformance/cases/*.json")
	if len(files) < 15 {
		t.Fatalf("only %d suites", len(files))
	}
	total := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var s suite
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		t.Run(strings.TrimSuffix(filepath.Base(f), ".json"), func(t *testing.T) {
			c := loadTestCatalog(t, s.Catalog)
			for _, tc := range s.Tests {
				want := tc.Valid == nil || *tc.Valid
				err := schemaValid(t, c, s.Schema, tc.Data)
				if (err == nil) != want {
					t.Errorf("%s: valid=%v, want %v\n%v", tc.Description, err == nil, want, err)
				}
				// What the schema refuses, the validator refuses, with errors.
				if !want {
					var errs []ValidationError
					switch s.Schema {
					case "", "agent_to_renderer.json":
						errs = c.Validate(tc.Data)
					case "renderer_to_agent.json":
						errs = c.ValidateEvent(tc.Data)
					default:
						errs = []ValidationError{{}}
					}
					if len(errs) == 0 {
						t.Errorf("%s: Validate found nothing", tc.Description)
					}
				}
				total++
			}
		})
	}
	if total < 150 {
		t.Errorf("ran %d cases", total)
	}
}

// The repository's example stream is valid, and passes this package's
// own checks too: one root, references that resolve.
func TestConformanceExampleStream(t *testing.T) {
	c := loadTestCatalog(t, "")
	f, err := os.Open("testdata/conformance/cases/contact_form_example.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []json.RawMessage
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		if line := bytes.TrimSpace(sc.Bytes()); len(line) > 0 {
			if err := schemaValid(t, c, "", line); err != nil {
				t.Errorf("line %d: %v", len(lines)+1, err)
			}
			lines = append(lines, bytes.Clone(line))
		}
	}
	all, _ := json.Marshal(lines)
	if errs := c.Validate(all); len(errs) > 0 {
		t.Errorf("stream: %v", errs)
	}
}

// The catalogs of the conformance runner pass the catalog rules (it checks
// them with catalog_definition.json, identifiers and $ref targets).
func TestConformanceCatalogs(t *testing.T) {
	for _, name := range []string{"basic_catalog.json", "testing_catalog.json"} {
		loadTestCatalog(t, name)
	}
}
