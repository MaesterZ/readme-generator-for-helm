// readme_generator.go
// A Go re‑implementation of the Helm README & OpenAPI generator originally written in Node.js.
// It preserves the same command‑line interface:
//   -v|--values <values.yaml>
//   -r|--readme <README.md>
//   -c|--config <config.json>
//   -s|--schema <schema.json>
//   --version
//
// The program parses metadata comments inside the Helm values.yaml, validates them, updates
// the "## Parameters" section of the README with a Markdown table and optionally generates
// an OpenAPI v3 schema describing the values.
//
// Usage example:
//      readme-generator -v values.yaml -r README.md -s values.schema.json
//
// The implementation tries to follow the structure of the original project while adopting
// Go idioms.

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	yaml "gopkg.in/yaml.v3"
)

//-------------------------------------------------------------------------
// Version – overridden at build time with:  go build -ldflags "-X main.version=1.2.3"
//-------------------------------------------------------------------------

var version = "dev"

//-------------------------------------------------------------------------
// Command‑line options
//-------------------------------------------------------------------------

type options struct {
	valuesPath string
	readmePath string
	configPath string
	schemaPath string
	version    bool
}

func parseFlags() (*options, error) {
	opts := &options{}
	flag.StringVar(&opts.valuesPath, "values", "", "Path to values.yaml file")
	flag.StringVar(&opts.valuesPath, "v", "", "Path to values.yaml file (shorthand)")
	flag.StringVar(&opts.readmePath, "readme", "", "Path to README.md file")
	flag.StringVar(&opts.readmePath, "r", "", "Path to README.md file (shorthand)")
	flag.StringVar(&opts.configPath, "config", "", "Path to config.json file")
	flag.StringVar(&opts.configPath, "c", "", "Path to config.json file (shorthand)")
	flag.StringVar(&opts.schemaPath, "schema", "", "Path to OpenAPI schema output file")
	flag.StringVar(&opts.schemaPath, "s", "", "Path to OpenAPI schema output file (shorthand)")
	flag.BoolVar(&opts.version, "version", false, "Show generator version")
	flag.Parse()

	if opts.version {
		return opts, nil
	}

	if opts.valuesPath == "" {
		return nil, errors.New("--values is required")
	}
	if opts.readmePath == "" && opts.schemaPath == "" {
		return nil, errors.New("nothing to do – provide --readme and/or --schema")
	}

	// Default config path next to executable
	if opts.configPath == "" {
		exe, _ := os.Executable()
		opts.configPath = filepath.Join(filepath.Dir(exe), "config.json")
	}
	return opts, nil
}

//-------------------------------------------------------------------------
// Data structures (mirrors the JS classes)
//-------------------------------------------------------------------------

type Parameter struct {
	Name        string // dot‑notation path, e.g. image.repository
	Description string
	Value       interface{}
	Type        string
	Modifiers   []string
	Section     string

	Validate bool
	Readme   bool
	Schema   bool

	// RawValue is the YAML subtree of a param without its own leaf (e.g. an [array] of objects), for the schema
	RawValue interface{}
}

func NewParameter(name string) *Parameter {
	return &Parameter{
		Name:     name,
		Validate: true,
		Readme:   true,
		Schema:   true,
	}
}

func (p *Parameter) HasModifier(m string) bool {
	for _, mm := range p.Modifiers {
		if mm == m {
			return true
		}
	}
	return false
}

// Extra behaves like JS getter/setter pair. Simpler with bool field.
func (p *Parameter) SetExtra(b bool) {
	if b {
		p.Validate = false
		p.Readme = true
	}
}

func (p *Parameter) Extra() bool { return !p.Validate && p.Readme }

func (p *Parameter) SetSkip(b bool) {
	if b {
		p.Validate = false
		p.Readme = false
	} else {
		p.Validate = true
		p.Readme = true
	}
}

func (p *Parameter) Skip() bool { return !p.Validate && !p.Readme }

//-------------------------------------------------------------------------

type Section struct {
	Name             string
	DescriptionLines []string
	Parameters       []*Parameter
}

func (s *Section) Description() string { return strings.Join(s.DescriptionLines, "\n") }

//-------------------------------------------------------------------------

type Metadata struct {
	Sections   []*Section
	Parameters []*Parameter
}

func (m *Metadata) AddSection(sec *Section)   { m.Sections = append(m.Sections, sec) }
func (m *Metadata) AddParameter(p *Parameter) { m.Parameters = append(m.Parameters, p) }

//-------------------------------------------------------------------------
// Config JSON
//-------------------------------------------------------------------------

type Config struct {
	Comments struct {
		Format string `json:"format"`
	} `json:"comments"`
	Tags struct {
		Param            string `json:"param"`
		Section          string `json:"section"`
		DescriptionStart string `json:"descriptionStart"`
		DescriptionEnd   string `json:"descriptionEnd"`
		Skip             string `json:"skip"`
		Extra            string `json:"extra"`
	} `json:"tags"`
	Regexp struct {
		ParamsSectionTitle string `json:"paramsSectionTitle"`
	} `json:"regexp"`
	Modifiers struct {
		Array    string `json:"array"`
		Object   string `json:"object"`
		String   string `json:"string"`
		Nullable string `json:"nullable"`
		Default  string `json:"default"`
	} `json:"modifiers"`
}

// defaultConfig returns the built-in defaults that are used when
// no explicit config file is present.
func defaultConfig() *Config {
	cfg := &Config{}
	cfg.Comments.Format = "##"

	cfg.Tags.Param = "@param"
	cfg.Tags.Section = "@section"
	cfg.Tags.DescriptionStart = "@descriptionStart"
	cfg.Tags.DescriptionEnd = "@descriptionEnd"
	cfg.Tags.Skip = "@skip"
	cfg.Tags.Extra = "@extra"

	cfg.Modifiers.Array = "array"
	cfg.Modifiers.Object = "object"
	cfg.Modifiers.String = "string"
	cfg.Modifiers.Nullable = "nullable"
	cfg.Modifiers.Default = "default"

	cfg.Regexp.ParamsSectionTitle = "Parameters"
	return cfg
}

func loadConfig(path string) (*Config, error) {
	cfg := defaultConfig()

	if path == "" {
		return cfg, nil
	}
	data, err := ioutil.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

//-------------------------------------------------------------------------
// YAML utilities – flatten structures into dot notation «key», arrays as key[0]
//-------------------------------------------------------------------------

// flatValue is a flattened YAML leaf; schema is false when a key in its path contains a dot.
type flatValue struct {
	value  interface{}
	schema bool
}

// flattenYAML flattens nested YAML to dot-notation keys (a.b[0].c)
func flattenYAML(prefix string, in interface{}, out map[string]flatValue, schema bool) {
	switch v := in.(type) {

	case map[string]interface{}:
		if len(v) == 0 {
			if prefix != "" {
				out[prefix] = flatValue{v, schema}
			}
			return
		}
		for k, val := range v {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			// Dotted keys (e.g. prometheus.io/scrape) can't be split back into a tree, keep them out of the schema
			flattenYAML(key, val, out, schema && !strings.Contains(k, "."))
		}

	case []interface{}:
		if len(v) == 0 || isPlainArray(v) {
			if prefix != "" {
				out[prefix] = flatValue{v, schema}
			}
			return
		}
		for i, val := range v {
			key := fmt.Sprintf("%s[%d]", prefix, i)
			flattenYAML(key, val, out, schema)
		}

	default:
		out[prefix] = flatValue{v, schema}
	}
}

// isPlainArray reports whether all elements are strings; such arrays are documented as a single key.
func isPlainArray(arr []interface{}) bool {
	for _, e := range arr {
		if _, ok := e.(string); !ok {
			return false
		}
	}
	return true
}

//-------------------------------------------------------------------------
// createValuesObject – converts YAML to []*Parameter with value & type info
//-------------------------------------------------------------------------

func createValuesObject(valuesPath string) ([]*Parameter, interface{}, error) {
	raw, err := ioutil.ReadFile(valuesPath)
	if err != nil {
		return nil, nil, err
	}
	var node interface{}
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return nil, nil, err
	}

	m := map[string]flatValue{}
	flattenYAML("", node, m, true)

	// Build parameters
	params := []*Parameter{}
	for path, fv := range m {
		p := NewParameter(path)
		p.Value = fv.value
		// YAML null is rendered as Go's "nil"
		if fv.value == nil {
			p.Value = "nil"
		}
		p.Type = inferType(fv.value)
		p.Schema = fv.schema
		params = append(params, p)
	}
	// Sort for deterministic output
	sort.Slice(params, func(i, j int) bool { return params[i].Name < params[j].Name })
	return params, node, nil
}

func inferType(v interface{}) string {
	switch v.(type) {
	case nil:
		return "object" // matches JS typeof null
	case string:
		return "string"
	case bool:
		return "boolean"
	case int, int64, float64:
		return "number"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	default:
		return "unknown"
	}
}

//-------------------------------------------------------------------------
// utils helpers similar to lib/utils.js
//-------------------------------------------------------------------------

func getArrayPrefix(path string) string {
	idx := strings.Index(path, "[")
	if idx == -1 {
		return path
	}
	return path[:idx]
}

func sanitizeProperty(path string) string {
	if strings.Contains(path, "[") {
		return getArrayPrefix(path)
	}
	return path
}

//-------------------------------------------------------------------------
// parseMetadataComments – reads YAML file line by line and extracts @param, @section etc.
//-------------------------------------------------------------------------

func parseMetadataComments(valuesPath string, cfg *Config) (*Metadata, error) {
	f, err := os.Open(valuesPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	reader := bufio.NewReader(f)

	m := &Metadata{}
	var current *Section
	var descriptionMode bool

	// Pre‑build regexps
	regParam := regexp.MustCompile(fmt.Sprintf(`^\s*%s\s*%s\s*([^\s]+)\s*(\[.*?\])?\s*(.*)$`,
		regexp.QuoteMeta(cfg.Comments.Format), regexp.QuoteMeta(cfg.Tags.Param)))
	regSection := regexp.MustCompile(fmt.Sprintf(`^\s*%s\s*%s\s*(.*)$`,
		regexp.QuoteMeta(cfg.Comments.Format), regexp.QuoteMeta(cfg.Tags.Section)))
	regDescStart := regexp.MustCompile(fmt.Sprintf(`^\s*%s\s*%s\s*(.*)$`,
		regexp.QuoteMeta(cfg.Comments.Format), regexp.QuoteMeta(cfg.Tags.DescriptionStart)))
	regDescEnd := regexp.MustCompile(fmt.Sprintf(`^\s*%s\s*%s`,
		regexp.QuoteMeta(cfg.Comments.Format), regexp.QuoteMeta(cfg.Tags.DescriptionEnd)))
	regDescContent := regexp.MustCompile(fmt.Sprintf(`^\s*%s\s?(.*)`, regexp.QuoteMeta(cfg.Comments.Format)))
	regSkip := regexp.MustCompile(fmt.Sprintf(`^\s*%s\s*%s\s*([^\s]+).*`,
		regexp.QuoteMeta(cfg.Comments.Format), regexp.QuoteMeta(cfg.Tags.Skip)))
	regExtra := regexp.MustCompile(fmt.Sprintf(`^\s*%s\s*%s\s*([^\s]+)\s*(\[.*?\])?\s*(.*)$`,
		regexp.QuoteMeta(cfg.Comments.Format), regexp.QuoteMeta(cfg.Tags.Extra)))

	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		trimmed := strings.TrimRight(line, "\r\n")

		switch {
		case regSection.MatchString(trimmed):
			name := strings.TrimSpace(regSection.FindStringSubmatch(trimmed)[1])
			current = &Section{Name: name}
			m.AddSection(current)
			descriptionMode = false

		case regDescStart.MatchString(trimmed):
			descriptionMode = true
			if current != nil {
				first := regDescStart.FindStringSubmatch(trimmed)[1]
				if first != "" {
					current.DescriptionLines = append(current.DescriptionLines, first)
				}
			}

		case descriptionMode && regDescEnd.MatchString(trimmed):
			descriptionMode = false

		case descriptionMode && regDescContent.MatchString(trimmed):
			if current != nil {
				txt := regDescContent.FindStringSubmatch(trimmed)[1]
				current.DescriptionLines = append(current.DescriptionLines, txt)
			}

		case regParam.MatchString(trimmed):
			sm := regParam.FindStringSubmatch(trimmed)
			p := NewParameter(sm[1])
			mods := strings.Trim(sm[2], "[]")
			if mods != "" {
				for _, mmm := range strings.Split(mods, ",") {
					p.Modifiers = append(p.Modifiers, strings.TrimSpace(mmm))
				}
			}
			p.Description = sm[3]
			if current != nil {
				p.Section = current.Name
				current.Parameters = append(current.Parameters, p)
			}
			m.AddParameter(p)

		case regSkip.MatchString(trimmed):
			name := regSkip.FindStringSubmatch(trimmed)[1]
			p := NewParameter(name)
			p.SetSkip(true)
			if current != nil {
				p.Section = current.Name
				current.Parameters = append(current.Parameters, p)
			}
			m.AddParameter(p)

		case regExtra.MatchString(trimmed):
			sm := regExtra.FindStringSubmatch(trimmed)
			p := NewParameter(sm[1])
			p.Description = sm[3]
			p.Value = "" // empty string
			p.SetExtra(true)
			if current != nil {
				p.Section = current.Name
				current.Parameters = append(current.Parameters, p)
			}
			m.AddParameter(p)
		}

		if err == io.EOF {
			break
		}
	}
	return m, nil
}

//-------------------------------------------------------------------------
// checker – verifies that metadata ↔ actual keys match
//-------------------------------------------------------------------------

// checkKeys verifies that each actual YAML key has matching metadata and vice-versa,
// but skips entire sub-trees for parameters marked with @skip or any modifier.
func checkKeys(real []*Parameter, meta []*Parameter) error {
	// names that cancel validation for themselves and their children
	skipNames := map[string]struct{}{}
	for _, p := range meta {
		if p.Skip() || len(p.Modifiers) > 0 { // modifier implies object/array parent
			skipNames[sanitizeProperty(p.Name)] = struct{}{}
		}
	}

	// helper: does name fall under a skipped prefix?
	isSkipped := func(name string) bool {
		for sk := range skipNames {
			if name == sk ||
				strings.HasPrefix(name, sk+".") ||
				strings.HasPrefix(name, sk+"[") {
				return true
			}
		}
		return false
	}

	realKeys, metaKeys := []string{}, []string{}
	for _, p := range real {
		if !isSkipped(p.Name) && !p.Extra() {
			realKeys = append(realKeys, p.Name)
		}
	}
	for _, p := range meta {
		if !p.Extra() && !isSkipped(p.Name) {
			metaKeys = append(metaKeys, p.Name)
		}
	}

	missing := difference(realKeys, metaKeys) // present in YAML, absent in metadata
	orphan := difference(metaKeys, realKeys)  // present in metadata, absent in YAML

	if len(missing) == 0 && len(orphan) == 0 {
		fmt.Println("INFO: Metadata is correct!")
		return nil
	}
	for _, m := range missing {
		fmt.Printf("ERROR: Missing metadata for key: %s\n", m)
	}
	for _, o := range orphan {
		fmt.Printf("ERROR: Metadata provided for non existing key: %s\n", o)
	}
	return errors.New("metadata errors found")
}

func difference(a, b []string) []string {
	m := map[string]struct{}{}
	for _, x := range b {
		m[x] = struct{}{}
	}
	var diff []string
	for _, x := range a {
		if _, ok := m[x]; !ok {
			diff = append(diff, x)
		}
	}
	return diff
}

//-------------------------------------------------------------------------
// builder – combine values & metadata, apply modifiers, etc.
//-------------------------------------------------------------------------

func combineMetadataAndValues(values []*Parameter, meta []*Parameter) {
	for _, p := range meta {
		if p.Extra() { // skip
			continue
		}
		for _, src := range values {
			if src.Name == p.Name {
				if p.Value == nil {
					p.Value = src.Value
				}
				p.Type = src.Type
				p.Schema = src.Schema
				break
			}
		}
	}

	// Add skip parameters that are only in values (objects without @param)
	for _, src := range values {
		found := false
		for _, p := range meta {
			if p.Name == src.Name {
				found = true
				break
			}
		}
		if !found {
			// Insert after the closest parent
			np := *src
			np.SetSkip(true)
			meta = append(meta, &np)
		}
	}
}

// setRawValues stores the YAML subtree for modifier params that have no leaf of their own.
func setRawValues(meta []*Parameter, root interface{}) {
	for _, p := range meta {
		if p.Value == nil && len(p.Modifiers) > 0 && !p.Extra() {
			if v, ok := lookupPath(root, p.Name); ok {
				p.RawValue = v
			}
		}
	}
}

var pathSegmentRe = regexp.MustCompile(`^([^\[]*)((?:\[\d+\])*)$`)
var pathIndexRe = regexp.MustCompile(`\[(\d+)\]`)

// lookupPath resolves a dot-notation path like a.b[0].c; dotted keys aren't supported.
func lookupPath(node interface{}, path string) (interface{}, bool) {
	cur := node
	for _, seg := range strings.Split(path, ".") {
		m := pathSegmentRe.FindStringSubmatch(seg)
		if m == nil {
			return nil, false
		}
		if m[1] != "" {
			obj, ok := cur.(map[string]interface{})
			if !ok {
				return nil, false
			}
			if cur, ok = obj[m[1]]; !ok {
				return nil, false
			}
		}
		for _, idx := range pathIndexRe.FindAllStringSubmatch(m[2], -1) {
			arr, ok := cur.([]interface{})
			i, _ := strconv.Atoi(idx[1])
			if !ok || i >= len(arr) {
				return nil, false
			}
			cur = arr[i]
		}
	}
	return cur, true
}

// applyModifiers only needs array/object/string/nullable/default for README/schema rendering.
func applyModifiers(p *Parameter, cfg *Config) {
	if len(p.Modifiers) == 0 {
		return
	}
	nullableLast := false
	if p.HasModifier(cfg.Modifiers.Nullable) && p.Modifiers[len(p.Modifiers)-1] == cfg.Modifiers.Nullable {
		nullableLast = true
	}
	for _, m := range p.Modifiers {
		switch m {
		case cfg.Modifiers.Array:
			p.Type = "array"
			if !nullableLast {
				p.Value = []interface{}{}
			}
		case cfg.Modifiers.Object:
			p.Type = "object"
			if !nullableLast {
				p.Value = map[string]interface{}{}
			}
		case cfg.Modifiers.String:
			p.Type = "string"
			if !nullableLast {
				p.Value = ""
			}
		case cfg.Modifiers.Nullable:
			if p.Value == nil {
				p.Value = "nil"
			}
		default:
			// default:<val>
			if strings.HasPrefix(m, cfg.Modifiers.Default+":") {
				p.Value = strings.TrimSpace(strings.TrimPrefix(m, cfg.Modifiers.Default+":"))
			}
		}
	}
}

// applySchemaModifiers sets types from modifiers but keeps the real YAML value; README placeholders are
// only used when the key has no value. [default: X] still overrides.
func applySchemaModifiers(p *Parameter, cfg *Config) {
	var placeholder interface{}
	for _, m := range p.Modifiers {
		switch m {
		case cfg.Modifiers.Array:
			p.Type, placeholder = "array", []interface{}{}
		case cfg.Modifiers.Object:
			p.Type, placeholder = "object", map[string]interface{}{}
		case cfg.Modifiers.String:
			p.Type, placeholder = "string", ""
		default:
			if strings.HasPrefix(m, cfg.Modifiers.Default+":") {
				p.Value = strings.TrimSpace(strings.TrimPrefix(m, cfg.Modifiers.Default+":"))
			}
		}
	}
	if p.Value == nil {
		p.Value = p.RawValue
	}
	if p.Value == nil {
		if p.HasModifier(cfg.Modifiers.Nullable) {
			p.Value = "nil"
		} else {
			p.Value = placeholder
		}
	}
}

// buildParamsToRender returns modified copies of the non-skipped params, leaving list untouched.
func buildParamsToRender(list []*Parameter, cfg *Config, apply func(*Parameter, *Config)) []*Parameter {
	out := []*Parameter{}
	for _, p := range list {
		if p.Skip() {
			continue
		}
		c := *p
		apply(&c, cfg)
		out = append(out, &c)
	}
	return out
}

//-------------------------------------------------------------------------
// Rendering helpers
//-------------------------------------------------------------------------

func markdownTable(params []*Parameter) string {
	rows := [][]string{{"Name", "Description", "Value"}}

	for _, p := range params {
		val := ""
		if !p.Extra() {
			switch vv := p.Value.(type) {
			case string:
				if vv == "" {
					val = "`\"\"`"
				} else {
					val = fmt.Sprintf("`%s`", vv)
				}
			default:
				val = fmt.Sprintf("`%s`", jsonString(vv))
			}
		}
		rows = append(rows, []string{
			fmt.Sprintf("`%s`", p.Name),
			p.Description,
			val,
		})
	}

	w := make([]int, len(rows[0]))
	for _, r := range rows {
		for i, c := range r {
			if l := len(c); l > w[i] {
				w[i] = l
			}
		}
	}

	var b strings.Builder
	for i, r := range rows {
		b.WriteString("|")
		for j, c := range r {
			b.WriteString(" ")
			b.WriteString(c)
			b.WriteString(strings.Repeat(" ", w[j]-len(c)))
			b.WriteString(" |")
		}
		b.WriteString("\n")

		if i == 0 {
			b.WriteString("|")
			for _, ww := range w {
				b.WriteString(" ")
				b.WriteString(strings.Repeat("-", ww))
				b.WriteString(" |")
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// jsonString encodes v like JS JSON.stringify, without escaping <, > and &.
func jsonString(v interface{}) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}

func renderSection(sec *Section, h string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s %s\n\n", h, sec.Name))

	if d := strings.TrimRight(sec.Description(), "\n"); d != "" {
		b.WriteString(d)
		b.WriteString("\n\n")
	}

	if len(sec.Parameters) > 0 {
		b.WriteString(markdownTable(sec.Parameters))
	}
	return b.String()
}

func renderReadmeTable(secs []*Section, h string) string {
	var b strings.Builder
	for _, s := range secs {
		b.WriteString("\n")
		b.WriteString(renderSection(s, h))
	}
	return b.String()
}

// insertReadmeTable – replaces existing Parameters section or appends it
func insertReadmeTable(readmePath string, sections []*Section, cfg *Config) error {
	raw, err := ioutil.ReadFile(readmePath)
	if err != nil {
		return err
	}
	lines := newlineRe.Split(string(raw), -1)

	// Find start of parameters section (level ##+ heading matching cfg.Regexp.ParamsSectionTitle)
	start := -1
	hPrefix := "##" // default – overwritten when we detect exact hashes
	reStart := regexp.MustCompile(fmt.Sprintf(`^(##+) %s`, cfg.Regexp.ParamsSectionTitle))
	for i, l := range lines {
		if m := reStart.FindStringSubmatch(l); m != nil {
			start = i + 1        // insert after header line
			hPrefix = m[1] + "#" // child headings get one more '#'
			break
		}
	}

	if start == -1 {
		return errors.New("could not find Parameters section in README")
	}

	// Find end = next header of same level or EOF
	end := len(lines)
	sameLevel := regexp.MustCompile(fmt.Sprintf(`^%s\s`, strings.Repeat("#", len(hPrefix)-1)))
	for i := start; i < len(lines); i++ {
		if sameLevel.MatchString(lines[i]) {
			end = i
			break
		}
	}

	// Replace up to the last table-like line (plus the blank line after it), keeping any text below the tables.
	// Without a table, only the line right after the header is replaced.
	replaceEnd := start + 1
	for i := end - 1; i >= start; i-- {
		if lines[i] != "" && !isTextLine(lines[i]) {
			replaceEnd = i + 2
			break
		}
	}
	if replaceEnd > len(lines) {
		replaceEnd = len(lines)
	}

	newTable := renderReadmeTable(sections, hPrefix)
	newLines := append([]string{}, lines[:start]...)
	newLines = append(newLines, strings.Split(newTable, "\n")...)
	newLines = append(newLines, lines[replaceEnd:]...)

	return ioutil.WriteFile(readmePath, []byte(strings.Join(newLines, "\n")), 0644)
}

var newlineRe = regexp.MustCompile(`\r?\n`)

// isTextLine reports whether a line is prose rather than a table row or a heading.
func isTextLine(line string) bool {
	if strings.Contains(line, "|") {
		return false
	}
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line) != ""
}

//-------------------------------------------------------------------------
// OpenAPI Schema – minimal implementation (object graph with default values)
//-------------------------------------------------------------------------

// schemaNode is a JSON object that keeps keys in insertion order, matching the Node.js output.
type schemaNode struct {
	keys []string
	vals map[string]interface{}
}

// newNode builds a node from alternating key/value pairs.
func newNode(kv ...interface{}) *schemaNode {
	n := &schemaNode{vals: map[string]interface{}{}}
	for i := 0; i+1 < len(kv); i += 2 {
		n.set(kv[i].(string), kv[i+1])
	}
	return n
}

// set adds or replaces a key; a replaced key keeps its original position, like a JS object.
func (n *schemaNode) set(k string, v interface{}) {
	if _, ok := n.vals[k]; !ok {
		n.keys = append(n.keys, k)
	}
	n.vals[k] = v
}

// child returns the node stored at k, if any.
func (n *schemaNode) child(k string) (*schemaNode, bool) {
	c, ok := n.vals[k].(*schemaNode)
	return c, ok
}

func (n *schemaNode) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteString("{")
	for i, k := range n.keys {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(jsonString(k))
		b.WriteString(":")
		b.WriteString(jsonString(n.vals[k]))
	}
	b.WriteString("}")
	return []byte(b.String()), nil
}

// arrayKeyRe matches a path component indexing an array of objects, e.g. jobs[0]
var arrayKeyRe = regexp.MustCompile(`^(.+)\[.+\]$`)

// addSchema writes the schema for param along tree (its dot-split name) into props.
// Inside arrays of objects the item defaults are ignored.
func addSchema(param *Parameter, tree []string, props *schemaNode, ignoreDefault bool, cfg *Config) error {
	for i, part := range tree {
		if i == len(tree)-1 {
			nullable := param.HasModifier(cfg.Modifiers.Nullable)
			var typ interface{} = param.Type
			if nullable && param.Type != "" {
				// Helm validates with JSON Schema, which ignores OpenAPI's nullable keyword
				typ = []string{param.Type, "null"}
			}
			obj := newNode("type", typ, "description", param.Description)
			if !ignoreDefault {
				obj.set("default", schemaDefault(param.Value))
			}
			if nullable {
				obj.set("nullable", true)
			}
			if param.Type == "array" {
				items := newNode()
				if arr, ok := param.Value.([]interface{}); ok && len(arr) > 0 {
					items.set("type", inferType(arr[0]))
				}
				obj.set("items", items)
			}
			props.set(part, obj)
			return nil
		}

		if m := arrayKeyRe.FindStringSubmatch(part); m != nil {
			key := m[1]
			if _, ok := props.vals[key]; !ok {
				props.set(key, newNode(
					"type", "array",
					"description", param.Description,
					"items", newNode("type", "object", "properties", newNode()),
				))
			}
			var itemProps *schemaNode
			arr, ok := props.child(key)
			if ok {
				if items, ok := arr.child("items"); ok {
					itemProps, ok = items.child("properties")
				}
			}
			if itemProps == nil {
				return fmt.Errorf("conflicting schema definitions for %s", param.Name)
			}
			return addSchema(param, tree[i+1:], itemProps, true, cfg)
		}

		if _, ok := props.vals[part]; !ok {
			props.set(part, newNode("type", "object", "properties", newNode()))
		}
		var next *schemaNode
		if obj, ok := props.child(part); ok {
			next, _ = obj.child("properties")
		}
		if next == nil {
			return fmt.Errorf("conflicting schema definitions for %s", param.Name)
		}
		props = next
	}
	return nil
}

// schemaDefault maps the "nil" and "null" placeholders to JSON null.
func schemaDefault(v interface{}) interface{} {
	if v == "nil" || v == "null" {
		return nil
	}
	return v
}

func renderOpenAPISchema(path string, params []*Parameter, cfg *Config) error {
	var nils []string
	for _, p := range params {
		if p.Value == "nil" && !p.HasModifier(cfg.Modifiers.Nullable) {
			nils = append(nils, p.Name)
		}
	}
	if len(nils) > 0 {
		return fmt.Errorf("invalid type 'nil' for the following values: %s", strings.Join(nils, ", "))
	}

	props := newNode()
	root := newNode("title", "Chart Values", "type", "object", "properties", props)
	for _, p := range params {
		// Object modifier entries are README-only; params without a value only exist in the metadata
		if p.Extra() || !p.Schema || p.HasModifier(cfg.Modifiers.Object) || p.Value == nil {
			continue
		}
		if err := addSchema(p, strings.Split(p.Name, "."), props, false, cfg); err != nil {
			return err
		}
	}

	// Indent like JSON.stringify(schema, null, 4): no HTML escaping, no trailing newline
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "    ")
	if err := enc.Encode(root); err != nil {
		return err
	}
	return ioutil.WriteFile(path, []byte(strings.TrimSuffix(b.String(), "\n")), 0644)
}

//-------------------------------------------------------------------------
// getParsedMetadata combines everything like JS version
//-------------------------------------------------------------------------

func getParsedMetadata(valuesPath string, cfg *Config) (*Metadata, error) {
	valuesObj, root, err := createValuesObject(valuesPath)
	if err != nil {
		return nil, err
	}
	meta, err := parseMetadataComments(valuesPath, cfg)
	if err != nil {
		return nil, err
	}
	if err := checkKeys(valuesObj, meta.Parameters); err != nil {
		return nil, err
	}
	combineMetadataAndValues(valuesObj, meta.Parameters)
	setRawValues(meta.Parameters, root)
	return meta, nil
}

//-------------------------------------------------------------------------
// runReadmeGenerator – public entry similar to JS runReadmeGenerator
//-------------------------------------------------------------------------

func runReadmeGenerator(opts *options) error {
	if opts.version {
		fmt.Println("Version:", version)
		return nil
	}

	cfg, err := loadConfig(opts.configPath)
	if err != nil {
		return err
	}

	meta, err := getParsedMetadata(opts.valuesPath, cfg)
	if err != nil {
		return err
	}

	if opts.readmePath != "" {
		for _, sec := range meta.Sections {
			sec.Parameters = buildParamsToRender(sec.Parameters, cfg, applyModifiers)
		}
		if err := insertReadmeTable(opts.readmePath, meta.Sections, cfg); err != nil {
			return err
		}
		fmt.Println("README updated ✅")
	}

	if opts.schemaPath != "" {
		meta.Parameters = buildParamsToRender(meta.Parameters, cfg, applySchemaModifiers)
		if err := renderOpenAPISchema(opts.schemaPath, meta.Parameters, cfg); err != nil {
			return err
		}
		fmt.Println("Schema generated ✅")
	}

	return nil
}

//-------------------------------------------------------------------------
// main
//-------------------------------------------------------------------------

func main() {
	opts, err := parseFlags()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := runReadmeGenerator(opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
