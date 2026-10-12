package main

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInferType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input any
		want  string
	}{
		{"nil", nil, "object"},
		{"string", "hello", "string"},
		{"bool", true, "boolean"},
		{"int", 1, "number"},
		{"int64", int64(1), "number"},
		{"float64", 1.5, "number"},
		{"array", []any{1, 2}, "array"},
		{"object", map[string]any{"a": 1}, "object"},
		{"unknown", struct{}{}, "unknown"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := inferType(testCase.input); got != testCase.want {
				t.Errorf("inferType(%v) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

func TestGetArrayPrefix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"no brackets", "a.b.c", "a.b.c"},
		{"single index", "a.b[0]", "a.b"},
		{"index not at start", "a[0].b", "a"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := getArrayPrefix(testCase.input); got != testCase.want {
				t.Errorf("getArrayPrefix(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

func TestSanitizeProperty(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"no brackets", "a.b.c", "a.b.c"},
		{"with index", "a.b[0].c", "a.b"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := sanitizeProperty(testCase.input); got != testCase.want {
				t.Errorf("sanitizeProperty(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

func TestDifference(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a, b []string
		want []string
	}{
		{"no overlap", []string{"a", "b"}, []string{"c"}, []string{"a", "b"}},
		{"full overlap", []string{"a", "b"}, []string{"a", "b"}, nil},
		{"partial overlap", []string{"a", "b", "c"}, []string{"b"}, []string{"a", "c"}},
		{"empty a", nil, []string{"a"}, nil},
		{"empty b", []string{"a"}, nil, []string{"a"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := difference(testCase.a, testCase.b)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("difference(%v, %v) = %v, want %v", testCase.a, testCase.b, got, testCase.want)
			}
		})
	}
}

// writeFile writes content to path, failing the test on error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()

	err := os.WriteFile(path, []byte(content), 0o600) //nolint:gosec // tests only write into t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
}

// runOn writes values to a temp dir and runs the generator with the given README and schema outputs.
func runOn(t *testing.T, values, readme string, withSchema bool) (string, map[string]any, error) {
	t.Helper()

	dir := t.TempDir()
	opts := &options{valuesPath: filepath.Join(dir, "values.yaml")}
	writeFile(t, opts.valuesPath, values)

	if readme != "" {
		opts.readmePath = filepath.Join(dir, "README.md")
		writeFile(t, opts.readmePath, readme)
	}

	if withSchema {
		opts.schemaPath = filepath.Join(dir, "schema.json")
	}

	err := runReadmeGenerator(opts)
	if err != nil {
		return "", nil, err
	}

	var out string

	if readme != "" {
		raw, _ := os.ReadFile(opts.readmePath)
		out = string(raw)
	}

	var schema map[string]any

	if withSchema {
		raw, _ := os.ReadFile(opts.schemaPath)

		err = json.Unmarshal(raw, &schema)
		if err != nil {
			t.Fatal(err)
		}
	}

	return out, schema, nil
}

// prop walks schema properties by key, e.g. prop(schema, "jobs", "items", "name").
func prop(t *testing.T, schema map[string]any, path ...string) map[string]any {
	t.Helper()

	cur := schema

	for _, key := range path {
		if key == "items" {
			cur, _ = cur["items"].(map[string]any)
			continue
		}

		props, _ := cur["properties"].(map[string]any)

		next, ok := props[key].(map[string]any)
		if !ok {
			t.Fatalf("schema has no property %q along %v", key, path)
		}

		cur = next
	}

	return cur
}

// Golden README fixtures shared with the Node.js implementation.
func TestReadmeGolden(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, input, expected, config string }{
		{"first execution", "", "expected-readme.first-execution.md", ""},
		{"subsequent sections", "test-readme.md", "expected-readme.md", ""},
		{"last section", "test-readme.last-section.md", "expected-readme.last-section.md", ""},
		{"last section with text below", "test-readme.last-section-text-below.md", "expected-readme.last-section-text-below.md", ""},
		{"config file", "test-readme.config.md", "expected-readme.config.md", "test-config.json"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			input := "# Example\r\n\n## Parameters"

			if testCase.input != "" {
				raw, err := os.ReadFile(filepath.Join("testdata", testCase.input))
				if err != nil {
					t.Fatal(err)
				}

				input = string(raw)
			}

			readme := filepath.Join(t.TempDir(), "README.md")
			writeFile(t, readme, input)

			opts := &options{valuesPath: filepath.Join("testdata", "test-values.yaml"), readmePath: readme}
			if testCase.config != "" {
				opts.configPath = filepath.Join("testdata", testCase.config)
			}

			err := runReadmeGenerator(opts)
			if err != nil {
				t.Fatal(err)
			}

			got, _ := os.ReadFile(readme)

			want, _ := os.ReadFile(filepath.Join("testdata", testCase.expected))
			if string(got) != string(want) {
				t.Errorf("README mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

func TestPlainStringArray(t *testing.T) {
	t.Parallel()

	values := "## @section S\n## @param args Args\nargs:\n  - --foo\n  - \"<b>\"\n"

	readme, schema, err := runOn(t, values, "## Parameters\n", true)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(readme, "`[\"--foo\",\"<b>\"]`") {
		t.Errorf("README missing plain array value:\n%s", readme)
	}

	args := prop(t, schema, "args")
	if args["type"] != "array" || !reflect.DeepEqual(args["items"], map[string]any{"type": "string"}) {
		t.Errorf("unexpected args schema: %v", args)
	}
}

func TestReadmeKeepsTextAfterTables(t *testing.T) {
	t.Parallel()

	values := "## @section S\n## @param a A\na: 1\n"

	cases := []struct{ name, readme, keep string }{
		{"text below table", "## Parameters\n\n### Old\n\n| a | b |\n\nKeep me\n\n## Next\n", "Keep me"},
		{"no table", "## Parameters\n\nOnly prose\n", "Only prose"},
		{"crlf", "## Parameters\r\n\r\n| a | b |\r\n\r\nKeep me\r\n", "Keep me"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, _, err := runOn(t, values, testCase.readme, false)
			if err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(got, testCase.keep) || strings.Contains(got, "| a | b |") {
				t.Errorf("unexpected README:\n%s", got)
			}
		})
	}
}

func TestSchemaArrayOfObjects(t *testing.T) {
	t.Parallel()

	values := "## @section S\n## @param jobs[0].name Name\n## @param jobs[0].sub[0].x X\n" +
		"jobs:\n  - name: a\n    sub:\n      - x: 1\n"

	_, schema, err := runOn(t, values, "", true)
	if err != nil {
		t.Fatal(err)
	}

	jobs := prop(t, schema, "jobs")
	if jobs["type"] != "array" || jobs["description"] != "Name" {
		t.Errorf("unexpected jobs schema: %v", jobs)
	}

	name := prop(t, schema, "jobs", "items", "name")
	if _, ok := name["default"]; ok || name["type"] != "string" {
		t.Errorf("array item should have a type and no default: %v", name)
	}

	if x := prop(t, schema, "jobs", "items", "sub", "items", "x"); x["type"] != "number" {
		t.Errorf("unexpected nested item schema: %v", x)
	}
}

func TestSchemaSkipsDottedKeys(t *testing.T) {
	t.Parallel()

	values := "## @section S\n## @param annotations.prometheus.io/scrape Scrape\n## @param b B\n" +
		"annotations:\n  prometheus.io/scrape: \"true\"\nb: 1\n"

	_, schema, err := runOn(t, values, "", true)
	if err != nil {
		t.Fatal(err)
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties: %v", schema)
	}

	if _, ok := props["annotations"]; ok {
		t.Errorf("dotted key should not be in schema: %v", props)
	}

	if _, ok := props["b"]; !ok {
		t.Errorf("b missing from schema: %v", props)
	}
}

func TestSchemaRejectsNil(t *testing.T) {
	t.Parallel()

	_, _, err := runOn(t, "## @section S\n## @param a A\na: null\n", "", true)
	if err == nil || !strings.Contains(err.Error(), "invalid type 'nil'") {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestSchemaNullable(t *testing.T) {
	t.Parallel()

	values := "## @section S\n## @param a [nullable] A\n## @param b [string, nullable] B\na: null\n"

	readme, schema, err := runOn(t, values, "## Parameters\n", true)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(readme, "`nil`") {
		t.Errorf("README should show nil:\n%s", readme)
	}

	want := map[string][]any{"a": {"object", "null"}, "b": {"string", "null"}}
	for key, typ := range want {
		node := prop(t, schema, key)
		if d, ok := node["default"]; !ok || d != nil || node["nullable"] != true {
			t.Errorf("%s: want default null and nullable, got %v", key, node)
		}

		if !reflect.DeepEqual(node["type"], typ) {
			t.Errorf("%s: want type %v, got %v", key, typ, node["type"])
		}
	}
}

// The expected schema is Node's, except modifier params keep their real YAML value as default.
func TestSchemaGolden(t *testing.T) {
	t.Parallel()

	out := filepath.Join(t.TempDir(), "schema.json")
	opts := &options{valuesPath: filepath.Join("testdata", "test-values.yaml"), schemaPath: out}

	err := runReadmeGenerator(opts)
	if err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(out)

	want, _ := os.ReadFile(filepath.Join("testdata", "expected-schema.json"))
	if string(got) != string(want) {
		t.Errorf("schema mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestSchemaModifierDefaults(t *testing.T) {
	t.Parallel()

	values := "## @section S\n" +
		"## @param list [array] List\n## @param conf [string] Conf\n## @param objs [array] Objects\n" +
		"## @param reg [default: REGISTRY] Registry\n## @param absent [array] Not in values\n" +
		"list: [a, b]\nconf: \"x: 1\"\nobjs:\n  - w: x\nreg: docker.io\n"

	readme, schema, err := runOn(t, values, "## Parameters\n", true)
	if err != nil {
		t.Fatal(err)
	}
	// README rows keep the modifier placeholders
	for key, want := range map[string]string{"list": "`[]`", "conf": "`\"\"`", "reg": "`REGISTRY`"} {
		if !rowHas(readme, key, want) {
			t.Errorf("README row %s should contain %s:\n%s", key, want, readme)
		}
	}

	cases := []struct {
		key        string
		def, items any
	}{
		{"list", []any{"a", "b"}, map[string]any{"type": "string"}},
		{"conf", "x: 1", nil},
		{"objs", []any{map[string]any{"w": "x"}}, map[string]any{"type": "object"}},
		{"reg", "REGISTRY", nil},
		{"absent", []any{}, map[string]any{}},
	}
	for _, testCase := range cases {
		node := prop(t, schema, testCase.key)
		if !reflect.DeepEqual(node["default"], testCase.def) || !reflect.DeepEqual(node["items"], testCase.items) {
			t.Errorf("%s: got default %#v items %#v, want %#v %#v", testCase.key, node["default"], node["items"], testCase.def, testCase.items)
		}
	}
}

func TestSchemaNoHTMLEscape(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	values := filepath.Join(dir, "values.yaml")
	schema := filepath.Join(dir, "schema.json")

	writeFile(t, values, "## @section S\n## @param a Use <name> & co\na: \"<b>\"\n")

	err := runReadmeGenerator(&options{valuesPath: values, schemaPath: schema})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(schema)
	if !strings.Contains(string(got), `"Use <name> & co"`) || !strings.Contains(string(got), `"<b>"`) {
		t.Errorf("schema should not escape HTML:\n%s", got)
	}
}

// rowHas reports whether the README table row for key contains want.
func rowHas(readme, key, want string) bool {
	for line := range strings.SplitSeq(readme, "\n") {
		if strings.HasPrefix(line, "| `"+key+"`") {
			return strings.Contains(line, want)
		}
	}

	return false
}

func TestParseFlags(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		args    []string
		wantErr error
	}{
		{"no arguments shows help", nil, flag.ErrHelp},
		{"short help", []string{"-h"}, flag.ErrHelp},
		{"long help", []string{"--help"}, flag.ErrHelp},
		{"unknown flag", []string{"--nope"}, errInvalidFlags},
		{"missing values", []string{"-r", "README.md"}, errValuesRequired},
		{"nothing to do", []string{"-v", "values.yaml"}, errNothingToDo},
		{"version", []string{"--version"}, nil},
		{"readme", []string{"--values", "values.yaml", "-r", "README.md"}, nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			opts, err := parseFlags(testCase.args)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("parseFlags(%q) error = %v, want %v", testCase.args, err, testCase.wantErr)
			}

			if testCase.wantErr == nil && opts == nil {
				t.Errorf("parseFlags(%q) returned no options", testCase.args)
			}
		})
	}
}
