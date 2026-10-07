package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInferType(t *testing.T) {
	cases := []struct {
		name  string
		input interface{}
		want  string
	}{
		{"nil", nil, "object"},
		{"string", "hello", "string"},
		{"bool", true, "boolean"},
		{"int", 1, "number"},
		{"int64", int64(1), "number"},
		{"float64", 1.5, "number"},
		{"array", []interface{}{1, 2}, "array"},
		{"object", map[string]interface{}{"a": 1}, "object"},
		{"unknown", struct{}{}, "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := inferType(c.input); got != c.want {
				t.Errorf("inferType(%v) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestGetArrayPrefix(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"no brackets", "a.b.c", "a.b.c"},
		{"single index", "a.b[0]", "a.b"},
		{"index not at start", "a[0].b", "a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := getArrayPrefix(c.input); got != c.want {
				t.Errorf("getArrayPrefix(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestSanitizeProperty(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"no brackets", "a.b.c", "a.b.c"},
		{"with index", "a.b[0].c", "a.b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeProperty(c.input); got != c.want {
				t.Errorf("sanitizeProperty(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestDifference(t *testing.T) {
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
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := difference(c.a, c.b)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("difference(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

// runOn writes values to a temp dir and runs the generator with the given README and schema outputs.
func runOn(t *testing.T, values, readme string, schema bool) (string, map[string]any, error) {
	t.Helper()
	dir := t.TempDir()
	opts := &options{valuesPath: filepath.Join(dir, "values.yaml")}
	if err := os.WriteFile(opts.valuesPath, []byte(values), 0644); err != nil {
		t.Fatal(err)
	}
	if readme != "" {
		opts.readmePath = filepath.Join(dir, "README.md")
		if err := os.WriteFile(opts.readmePath, []byte(readme), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if schema {
		opts.schemaPath = filepath.Join(dir, "schema.json")
	}
	if err := runReadmeGenerator(opts); err != nil {
		return "", nil, err
	}
	var out string
	if readme != "" {
		b, _ := os.ReadFile(opts.readmePath)
		out = string(b)
	}
	var s map[string]any
	if schema {
		b, _ := os.ReadFile(opts.schemaPath)
		if err := json.Unmarshal(b, &s); err != nil {
			t.Fatal(err)
		}
	}
	return out, s, nil
}

// prop walks schema properties by key, e.g. prop(s, "jobs", "items", "name").
func prop(t *testing.T, s map[string]any, path ...string) map[string]any {
	t.Helper()
	cur := s
	for _, p := range path {
		if p == "items" {
			cur, _ = cur["items"].(map[string]any)
			continue
		}
		props, _ := cur["properties"].(map[string]any)
		next, ok := props[p].(map[string]any)
		if !ok {
			t.Fatalf("schema has no property %q along %v", p, path)
		}
		cur = next
	}
	return cur
}

// Golden README fixtures shared with the Node.js implementation.
func TestReadmeGolden(t *testing.T) {
	cases := []struct{ name, input, expected, config string }{
		{"first execution", "", "expected-readme.first-execution.md", ""},
		{"subsequent sections", "test-readme.md", "expected-readme.md", ""},
		{"last section", "test-readme.last-section.md", "expected-readme.last-section.md", ""},
		{"last section with text below", "test-readme.last-section-text-below.md", "expected-readme.last-section-text-below.md", ""},
		{"config file", "test-readme.config.md", "expected-readme.config.md", "test-config.json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			input := []byte("# Example\r\n\n## Parameters")
			if c.input != "" {
				var err error
				if input, err = os.ReadFile(filepath.Join("testdata", c.input)); err != nil {
					t.Fatal(err)
				}
			}
			readme := filepath.Join(t.TempDir(), "README.md")
			if err := os.WriteFile(readme, input, 0644); err != nil {
				t.Fatal(err)
			}
			opts := &options{valuesPath: filepath.Join("testdata", "test-values.yaml"), readmePath: readme}
			if c.config != "" {
				opts.configPath = filepath.Join("testdata", c.config)
			}
			if err := runReadmeGenerator(opts); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(readme)
			want, _ := os.ReadFile(filepath.Join("testdata", c.expected))
			if string(got) != string(want) {
				t.Errorf("README mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

func TestPlainStringArray(t *testing.T) {
	values := "## @section S\n## @param args Args\nargs:\n  - --foo\n  - \"<b>\"\n"
	readme, s, err := runOn(t, values, "## Parameters\n", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readme, "`[\"--foo\",\"<b>\"]`") {
		t.Errorf("README missing plain array value:\n%s", readme)
	}
	args := prop(t, s, "args")
	if args["type"] != "array" || !reflect.DeepEqual(args["items"], map[string]any{"type": "string"}) {
		t.Errorf("unexpected args schema: %v", args)
	}
}

func TestReadmeKeepsTextAfterTables(t *testing.T) {
	values := "## @section S\n## @param a A\na: 1\n"
	cases := []struct{ name, readme, keep string }{
		{"text below table", "## Parameters\n\n### Old\n\n| a | b |\n\nKeep me\n\n## Next\n", "Keep me"},
		{"no table", "## Parameters\n\nOnly prose\n", "Only prose"},
		{"crlf", "## Parameters\r\n\r\n| a | b |\r\n\r\nKeep me\r\n", "Keep me"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, err := runOn(t, values, c.readme, false)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(got, c.keep) || strings.Contains(got, "| a | b |") {
				t.Errorf("unexpected README:\n%s", got)
			}
		})
	}
}

func TestSchemaArrayOfObjects(t *testing.T) {
	values := "## @section S\n## @param jobs[0].name Name\n## @param jobs[0].sub[0].x X\n" +
		"jobs:\n  - name: a\n    sub:\n      - x: 1\n"
	_, s, err := runOn(t, values, "", true)
	if err != nil {
		t.Fatal(err)
	}
	jobs := prop(t, s, "jobs")
	if jobs["type"] != "array" || jobs["description"] != "Name" {
		t.Errorf("unexpected jobs schema: %v", jobs)
	}
	name := prop(t, s, "jobs", "items", "name")
	if _, ok := name["default"]; ok || name["type"] != "string" {
		t.Errorf("array item should have a type and no default: %v", name)
	}
	if x := prop(t, s, "jobs", "items", "sub", "items", "x"); x["type"] != "number" {
		t.Errorf("unexpected nested item schema: %v", x)
	}
}

func TestSchemaSkipsDottedKeys(t *testing.T) {
	values := "## @section S\n## @param annotations.prometheus.io/scrape Scrape\n## @param b B\n" +
		"annotations:\n  prometheus.io/scrape: \"true\"\nb: 1\n"
	_, s, err := runOn(t, values, "", true)
	if err != nil {
		t.Fatal(err)
	}
	props := s["properties"].(map[string]any)
	if _, ok := props["annotations"]; ok {
		t.Errorf("dotted key should not be in schema: %v", props)
	}
	if _, ok := props["b"]; !ok {
		t.Errorf("b missing from schema: %v", props)
	}
}

func TestSchemaNil(t *testing.T) {
	t.Run("rejects nil without nullable", func(t *testing.T) {
		_, _, err := runOn(t, "## @section S\n## @param a A\na: null\n", "", true)
		if err == nil || !strings.Contains(err.Error(), "invalid type 'nil'") {
			t.Errorf("expected nil error, got %v", err)
		}
	})
	t.Run("nullable renders null", func(t *testing.T) {
		values := "## @section S\n## @param a [nullable] A\n## @param b [string, nullable] B\na: null\n"
		readme, s, err := runOn(t, values, "## Parameters\n", true)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(readme, "`nil`") {
			t.Errorf("README should show nil:\n%s", readme)
		}
		for _, k := range []string{"a", "b"} {
			p := prop(t, s, k)
			if d, ok := p["default"]; !ok || d != nil || p["nullable"] != true {
				t.Errorf("%s: want default null and nullable, got %v", k, p)
			}
		}
		want := map[string][]any{"a": {"object", "null"}, "b": {"string", "null"}}
		for k, typ := range want {
			if p := prop(t, s, k); !reflect.DeepEqual(p["type"], typ) {
				t.Errorf("%s: want type %v, got %v", k, typ, p["type"])
			}
		}
	})
}

// The expected schema is Node's, except modifier params keep their real YAML value as default.
func TestSchemaGolden(t *testing.T) {
	out := filepath.Join(t.TempDir(), "schema.json")
	opts := &options{valuesPath: filepath.Join("testdata", "test-values.yaml"), schemaPath: out}
	if err := runReadmeGenerator(opts); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	want, _ := os.ReadFile(filepath.Join("testdata", "expected-schema.json"))
	if string(got) != string(want) {
		t.Errorf("schema mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestSchemaModifierDefaults(t *testing.T) {
	values := "## @section S\n" +
		"## @param list [array] List\n## @param conf [string] Conf\n## @param objs [array] Objects\n" +
		"## @param reg [default: REGISTRY] Registry\n## @param absent [array] Not in values\n" +
		"list: [a, b]\nconf: \"x: 1\"\nobjs:\n  - w: x\nreg: docker.io\n"
	readme, s, err := runOn(t, values, "## Parameters\n", true)
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
	for _, c := range cases {
		p := prop(t, s, c.key)
		if !reflect.DeepEqual(p["default"], c.def) || !reflect.DeepEqual(p["items"], c.items) {
			t.Errorf("%s: got default %#v items %#v, want %#v %#v", c.key, p["default"], p["items"], c.def, c.items)
		}
	}
}

func TestSchemaNoHTMLEscape(t *testing.T) {
	dir := t.TempDir()
	values := filepath.Join(dir, "values.yaml")
	schema := filepath.Join(dir, "schema.json")
	os.WriteFile(values, []byte("## @section S\n## @param a Use <name> & co\na: \"<b>\"\n"), 0644)
	if err := runReadmeGenerator(&options{valuesPath: values, schemaPath: schema}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(schema)
	if !strings.Contains(string(got), `"Use <name> & co"`) || !strings.Contains(string(got), `"<b>"`) {
		t.Errorf("schema should not escape HTML:\n%s", got)
	}
}

// rowHas reports whether the README table row for key contains want.
func rowHas(readme, key, want string) bool {
	for _, l := range strings.Split(readme, "\n") {
		if strings.HasPrefix(l, "| `"+key+"`") {
			return strings.Contains(l, want)
		}
	}
	return false
}
