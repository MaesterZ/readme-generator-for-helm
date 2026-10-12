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
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

//-------------------------------------------------------------------------
// Version – overridden at build time with:  go build -ldflags "-X main.version=1.2.3"
//-------------------------------------------------------------------------

var version = "dev"

// Value types as reported by JS typeof, plus the "nil" placeholder for YAML null.
const (
	typeArray   = "array"
	typeObject  = "object"
	typeString  = "string"
	typeNumber  = "number"
	typeBoolean = "boolean"
	nilValue    = "nil"
)

// generatedFileMode is used for the README and schema, which are committed alongside the chart.
const generatedFileMode = 0o644

var (
	errValuesRequired  = errors.New("--values is required")
	errNothingToDo     = errors.New("nothing to do – provide --readme and/or --schema")
	errMetadata        = errors.New("metadata errors found")
	errNoParamsSection = errors.New("could not find Parameters section in README")
	errSchemaConflict  = errors.New("conflicting schema definitions")
	errNilValues       = errors.New("invalid type 'nil' for the following values")
	errInvalidFlags    = errors.New("invalid arguments")
)

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

// usage is printed for -h, --help and when no arguments are given.
const usage = `Usage: readme-generator-for-helm [options]

Options:
  -v, --values  <file>   Path to the values.yaml file (required)
  -r, --readme  <file>   Path to the README.md file to update
  -c, --config  <file>   Path to config.json (optional; built-in defaults if omitted)
  -s, --schema  <file>   Path for the generated OpenAPI Schema
      --version          Print program version and exit
  -h, --help             Show help

At least one of --readme or --schema must be provided.
`

// parseFlags parses the command-line arguments (without the program name).
// It returns flag.ErrHelp when help is requested or no arguments are given.
func parseFlags(args []string) (*options, error) {
	opts := &options{}

	flags := flag.NewFlagSet("readme-generator-for-helm", flag.ContinueOnError)
	flags.SetOutput(io.Discard) // usage is printed by main
	flags.StringVar(&opts.valuesPath, "values", "", "")
	flags.StringVar(&opts.valuesPath, "v", "", "")
	flags.StringVar(&opts.readmePath, "readme", "", "")
	flags.StringVar(&opts.readmePath, "r", "", "")
	flags.StringVar(&opts.configPath, "config", "", "")
	flags.StringVar(&opts.configPath, "c", "", "")
	flags.StringVar(&opts.schemaPath, "schema", "", "")
	flags.StringVar(&opts.schemaPath, "s", "", "")
	flags.BoolVar(&opts.version, "version", false, "")

	if len(args) == 0 {
		return nil, flag.ErrHelp
	}

	err := flags.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return nil, flag.ErrHelp
	}

	if err != nil {
		return nil, fmt.Errorf("%w: %w", errInvalidFlags, err)
	}

	if opts.version {
		return opts, nil
	}

	if opts.valuesPath == "" {
		return nil, errValuesRequired
	}

	if opts.readmePath == "" && opts.schemaPath == "" {
		return nil, errNothingToDo
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
	Value       any
	Type        string
	Modifiers   []string
	Section     string

	Validate bool
	Readme   bool
	Schema   bool

	// RawValue is the YAML subtree of a param without its own leaf (e.g. an [array] of objects), for the schema
	RawValue any
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
	return slices.Contains(p.Modifiers, m)
}

// SetExtra marks an @extra parameter: rendered in the README but not validated.
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

	// The type modifiers are named after the types they set
	cfg.Modifiers.Array = typeArray
	cfg.Modifiers.Object = typeObject
	cfg.Modifiers.String = typeString
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

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}

		return nil, fmt.Errorf("read config: %w", err)
	}

	err = json.Unmarshal(data, cfg)
	if err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	return cfg, nil
}

//-------------------------------------------------------------------------
// YAML utilities – flatten structures into dot notation «key», arrays as key[0]
//-------------------------------------------------------------------------

// flatValue is a flattened YAML leaf; schema is false when a key in its path contains a dot.
type flatValue struct {
	value  any
	schema bool
}

// flattenYAML flattens nested YAML to dot-notation keys (a.b[0].c)
func flattenYAML(prefix string, in any, out map[string]flatValue, schema bool) {
	switch node := in.(type) {
	case map[string]any:
		flattenMap(prefix, node, out, schema)
	case []any:
		flattenSlice(prefix, node, out, schema)
	default:
		out[prefix] = flatValue{node, schema}
	}
}

func flattenMap(prefix string, node map[string]any, out map[string]flatValue, schema bool) {
	if len(node) == 0 {
		if prefix != "" {
			out[prefix] = flatValue{node, schema}
		}

		return
	}

	for key, val := range node {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		// Dotted keys (e.g. prometheus.io/scrape) can't be split back into a tree, keep them out of the schema
		flattenYAML(path, val, out, schema && !strings.Contains(key, "."))
	}
}

func flattenSlice(prefix string, node []any, out map[string]flatValue, schema bool) {
	if len(node) == 0 || isPlainArray(node) {
		if prefix != "" {
			out[prefix] = flatValue{node, schema}
		}

		return
	}

	for i, val := range node {
		flattenYAML(fmt.Sprintf("%s[%d]", prefix, i), val, out, schema)
	}
}

// isPlainArray reports whether all elements are strings; such arrays are documented as a single key.
func isPlainArray(arr []any) bool {
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

func createValuesObject(valuesPath string) ([]*Parameter, any, error) {
	raw, err := os.ReadFile(valuesPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read values: %w", err)
	}

	var node any

	err = yaml.Unmarshal(raw, &node)
	if err != nil {
		return nil, nil, fmt.Errorf("parse values %s: %w", valuesPath, err)
	}

	leaves := map[string]flatValue{}
	flattenYAML("", node, leaves, true)

	params := []*Parameter{}

	for path, leaf := range leaves {
		param := NewParameter(path)
		param.Value = leaf.value
		// YAML null is rendered as Go's "nil"
		if leaf.value == nil {
			param.Value = nilValue
		}

		param.Type = inferType(leaf.value)
		param.Schema = leaf.schema
		params = append(params, param)
	}
	// Sort for deterministic output
	sort.Slice(params, func(i, j int) bool { return params[i].Name < params[j].Name })

	return params, node, nil
}

func inferType(v any) string {
	switch v.(type) {
	case nil:
		return typeObject // matches JS typeof null
	case string:
		return typeString
	case bool:
		return typeBoolean
	case int, int64, float64:
		return typeNumber
	case []any:
		return typeArray
	case map[string]any:
		return typeObject
	default:
		return "unknown"
	}
}

//-------------------------------------------------------------------------
// utils helpers similar to lib/utils.js
//-------------------------------------------------------------------------

func getArrayPrefix(path string) string {
	prefix, _, _ := strings.Cut(path, "[")

	return prefix
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

// commentParser extracts sections and parameters from the metadata comments, one line at a time.
type commentParser struct {
	param, section, descStart, descEnd, descContent, skip, extra *regexp.Regexp

	meta            *Metadata
	current         *Section
	descriptionMode bool
}

func newCommentParser(cfg *Config) *commentParser {
	format := regexp.QuoteMeta(cfg.Comments.Format)
	tag := func(pattern, name string) *regexp.Regexp {
		return regexp.MustCompile(fmt.Sprintf(pattern, format, regexp.QuoteMeta(name)))
	}

	return &commentParser{
		param:       tag(`^\s*%s\s*%s\s*([^\s]+)\s*(\[.*?\])?\s*(.*)$`, cfg.Tags.Param),
		section:     tag(`^\s*%s\s*%s\s*(.*)$`, cfg.Tags.Section),
		descStart:   tag(`^\s*%s\s*%s\s*(.*)$`, cfg.Tags.DescriptionStart),
		descEnd:     tag(`^\s*%s\s*%s`, cfg.Tags.DescriptionEnd),
		descContent: regexp.MustCompile(fmt.Sprintf(`^\s*%s\s?(.*)`, format)),
		skip:        tag(`^\s*%s\s*%s\s*([^\s]+).*`, cfg.Tags.Skip),
		extra:       tag(`^\s*%s\s*%s\s*([^\s]+)\s*(\[.*?\])?\s*(.*)$`, cfg.Tags.Extra),
		meta:        &Metadata{},
	}
}

func (cp *commentParser) parseLine(line string) {
	if cp.parseSectionLine(line) {
		return
	}

	cp.parseParamLine(line)
}

// parseSectionLine handles @section and section descriptions, reporting whether the line was consumed.
func (cp *commentParser) parseSectionLine(line string) bool {
	if match := cp.section.FindStringSubmatch(line); match != nil {
		cp.current = &Section{Name: strings.TrimSpace(match[1])}
		cp.meta.AddSection(cp.current)
		cp.descriptionMode = false

		return true
	}

	if match := cp.descStart.FindStringSubmatch(line); match != nil {
		cp.descriptionMode = true
		if cp.current != nil && match[1] != "" {
			cp.current.DescriptionLines = append(cp.current.DescriptionLines, match[1])
		}

		return true
	}

	if !cp.descriptionMode {
		return false
	}

	if cp.descEnd.MatchString(line) {
		cp.descriptionMode = false

		return true
	}

	match := cp.descContent.FindStringSubmatch(line)
	if match == nil {
		return false
	}

	if cp.current != nil {
		cp.current.DescriptionLines = append(cp.current.DescriptionLines, match[1])
	}

	return true
}

// parseParamLine handles @param, @skip and @extra.
func (cp *commentParser) parseParamLine(line string) {
	if match := cp.param.FindStringSubmatch(line); match != nil {
		param := NewParameter(match[1])
		param.Modifiers = parseModifiers(match[2])
		param.Description = match[3]
		cp.add(param)

		return
	}

	if match := cp.skip.FindStringSubmatch(line); match != nil {
		param := NewParameter(match[1])
		param.SetSkip(true)
		cp.add(param)

		return
	}

	if match := cp.extra.FindStringSubmatch(line); match != nil {
		param := NewParameter(match[1])
		param.Description = match[3]
		param.Value = "" // empty string
		param.SetExtra(true)
		cp.add(param)
	}
}

// add records param in the metadata and in the current section, if any.
func (cp *commentParser) add(param *Parameter) {
	if cp.current != nil {
		param.Section = cp.current.Name
		cp.current.Parameters = append(cp.current.Parameters, param)
	}

	cp.meta.AddParameter(param)
}

// parseModifiers splits "[a, b]" into its trimmed modifiers.
func parseModifiers(list string) []string {
	mods := strings.Trim(list, "[]")
	if mods == "" {
		return nil
	}

	var out []string
	for mod := range strings.SplitSeq(mods, ",") {
		out = append(out, strings.TrimSpace(mod))
	}

	return out
}

func parseMetadataComments(valuesPath string, cfg *Config) (*Metadata, error) {
	file, err := os.Open(valuesPath)
	if err != nil {
		return nil, fmt.Errorf("read values: %w", err)
	}
	defer file.Close()

	parser := newCommentParser(cfg)
	reader := bufio.NewReader(file)

	for {
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("read values %s: %w", valuesPath, err)
		}

		parser.parseLine(strings.TrimRight(line, "\r\n"))

		if errors.Is(err, io.EOF) {
			break
		}
	}

	return parser.meta, nil
}

//-------------------------------------------------------------------------
// checker – verifies that metadata ↔ actual keys match
//-------------------------------------------------------------------------

// checkKeys verifies that each actual YAML key has matching metadata and vice-versa,
// but skips entire sub-trees for parameters marked with @skip or any modifier.
func checkKeys(values []*Parameter, meta []*Parameter) error {
	skipped := skipChecker(meta)
	realKeys := checkedKeys(values, skipped)
	metaKeys := checkedKeys(meta, skipped)

	missing := difference(realKeys, metaKeys) // present in YAML, absent in metadata
	orphan := difference(metaKeys, realKeys)  // present in metadata, absent in YAML

	if len(missing) == 0 && len(orphan) == 0 {
		fmt.Println("INFO: Metadata is correct!")
		return nil
	}

	for _, key := range missing {
		fmt.Printf("ERROR: Missing metadata for key: %s\n", key)
	}

	for _, key := range orphan {
		fmt.Printf("ERROR: Metadata provided for non existing key: %s\n", key)
	}

	return errMetadata
}

// skipChecker reports whether a key is under a @skip param or a param with modifiers, whose subtrees aren't validated.
func skipChecker(meta []*Parameter) func(string) bool {
	skipNames := map[string]struct{}{}

	for _, param := range meta {
		if param.Skip() || len(param.Modifiers) > 0 { // modifier implies object/array parent
			skipNames[sanitizeProperty(param.Name)] = struct{}{}
		}
	}

	return func(name string) bool {
		for sk := range skipNames {
			if name == sk || strings.HasPrefix(name, sk+".") || strings.HasPrefix(name, sk+"[") {
				return true
			}
		}

		return false
	}
}

// checkedKeys returns the names of the params that are validated.
func checkedKeys(params []*Parameter, skipped func(string) bool) []string {
	keys := []string{}

	for _, param := range params {
		if !param.Extra() && !skipped(param.Name) {
			keys = append(keys, param.Name)
		}
	}

	return keys
}

func difference(from, remove []string) []string {
	set := map[string]struct{}{}
	for _, x := range remove {
		set[x] = struct{}{}
	}

	var diff []string

	for _, x := range from {
		if _, ok := set[x]; !ok {
			diff = append(diff, x)
		}
	}

	return diff
}

//-------------------------------------------------------------------------
// builder – combine values & metadata, apply modifiers, etc.
//-------------------------------------------------------------------------

func combineMetadataAndValues(values []*Parameter, meta []*Parameter) {
	for _, param := range meta {
		if param.Extra() { // skip
			continue
		}

		for _, src := range values {
			if src.Name == param.Name {
				if param.Value == nil {
					param.Value = src.Value
				}

				param.Type = src.Type
				param.Schema = src.Schema

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
func setRawValues(meta []*Parameter, root any) {
	for _, p := range meta {
		if p.Value == nil && len(p.Modifiers) > 0 && !p.Extra() {
			if v, ok := lookupPath(root, p.Name); ok {
				p.RawValue = v
			}
		}
	}
}

var (
	pathSegmentRe = regexp.MustCompile(`^([^\[]*)((?:\[\d+\])*)$`)
	pathIndexRe   = regexp.MustCompile(`\[(\d+)\]`)
)

// lookupPath resolves a dot-notation path like a.b[0].c; dotted keys aren't supported.
func lookupPath(node any, path string) (any, bool) {
	cur := node

	for seg := range strings.SplitSeq(path, ".") {
		match := pathSegmentRe.FindStringSubmatch(seg)
		if match == nil {
			return nil, false
		}

		if match[1] != "" {
			obj, ok := cur.(map[string]any)
			if !ok {
				return nil, false
			}

			if cur, ok = obj[match[1]]; !ok {
				return nil, false
			}
		}

		for _, idx := range pathIndexRe.FindAllStringSubmatch(match[2], -1) {
			arr, ok := cur.([]any)

			i, _ := strconv.Atoi(idx[1])
			if !ok || i >= len(arr) {
				return nil, false
			}

			cur = arr[i]
		}
	}

	return cur, true
}

// typeModifier returns the type and README placeholder set by an array, object or string modifier.
func typeModifier(mod string, cfg *Config) (string, any, bool) {
	switch mod {
	case cfg.Modifiers.Array:
		return typeArray, []any{}, true
	case cfg.Modifiers.Object:
		return typeObject, map[string]any{}, true
	case cfg.Modifiers.String:
		return typeString, "", true
	}

	return "", nil, false
}

// defaultModifier returns VALUE for a [default: VALUE] modifier.
func defaultModifier(mod string, cfg *Config) (string, bool) {
	prefix := cfg.Modifiers.Default + ":"
	if !strings.HasPrefix(mod, prefix) {
		return "", false
	}

	return strings.TrimSpace(strings.TrimPrefix(mod, prefix)), true
}

// applyModifiers sets the README type and placeholder value; a trailing nullable keeps the real value.
func applyModifiers(param *Parameter, cfg *Config) {
	if len(param.Modifiers) == 0 {
		return
	}

	nullableLast := param.Modifiers[len(param.Modifiers)-1] == cfg.Modifiers.Nullable

	for _, mod := range param.Modifiers {
		if typ, placeholder, ok := typeModifier(mod, cfg); ok {
			param.Type = typ
			if !nullableLast {
				param.Value = placeholder
			}

			continue
		}

		if mod == cfg.Modifiers.Nullable {
			if param.Value == nil {
				param.Value = nilValue
			}

			continue
		}

		if val, ok := defaultModifier(mod, cfg); ok {
			param.Value = val
		}
	}
}

// applySchemaModifiers sets types from modifiers but keeps the real YAML value; README placeholders are
// only used when the key has no value. [default: X] still overrides.
func applySchemaModifiers(param *Parameter, cfg *Config) {
	var placeholder any

	for _, mod := range param.Modifiers {
		if typ, ph, ok := typeModifier(mod, cfg); ok {
			param.Type, placeholder = typ, ph
		} else if val, ok := defaultModifier(mod, cfg); ok {
			param.Value = val
		}
	}

	if param.Value == nil {
		param.Value = param.RawValue
	}

	if param.Value == nil {
		if param.HasModifier(cfg.Modifiers.Nullable) {
			param.Value = nilValue
		} else {
			param.Value = placeholder
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

func markdownTable(params []*Parameter) (string, error) {
	rows := [][]string{{"Name", "Description", "Value"}}

	for _, param := range params {
		val, err := cellValue(param)
		if err != nil {
			return "", err
		}

		rows = append(rows, []string{"`" + param.Name + "`", param.Description, val})
	}

	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for col, cell := range row {
			widths[col] = max(widths[col], len(cell))
		}
	}

	var out strings.Builder

	for i, row := range rows {
		writeRow(&out, row, widths)

		if i == 0 {
			sep := make([]string, len(widths))
			for col, width := range widths {
				sep[col] = strings.Repeat("-", width)
			}

			writeRow(&out, sep, widths)
		}
	}

	return out.String(), nil
}

// cellValue renders the Value column of a README row.
func cellValue(param *Parameter) (string, error) {
	if param.Extra() {
		return "", nil
	}

	if s, ok := param.Value.(string); ok {
		if s == "" {
			return "`\"\"`", nil
		}

		return "`" + s + "`", nil
	}

	s, err := jsonString(param.Value)
	if err != nil {
		return "", fmt.Errorf("render value of %s: %w", param.Name, err)
	}

	return "`" + s + "`", nil
}

// writeRow writes one table row, padding each cell to its column width.
func writeRow(out *strings.Builder, row []string, widths []int) {
	out.WriteString("|")

	for col, cell := range row {
		out.WriteString(" " + cell + strings.Repeat(" ", widths[col]-len(cell)) + " |")
	}

	out.WriteString("\n")
}

// jsonString encodes val like JS JSON.stringify, without escaping <, > and &.
func jsonString(val any) (string, error) {
	var out strings.Builder

	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)

	err := enc.Encode(val)
	if err != nil {
		return "", fmt.Errorf("encode JSON: %w", err)
	}

	return strings.TrimSuffix(out.String(), "\n"), nil
}

func renderSection(sec *Section, h string) (string, error) {
	var out strings.Builder
	fmt.Fprintf(&out, "%s %s\n\n", h, sec.Name)

	if d := strings.TrimRight(sec.Description(), "\n"); d != "" {
		out.WriteString(d)
		out.WriteString("\n\n")
	}

	if len(sec.Parameters) > 0 {
		table, err := markdownTable(sec.Parameters)
		if err != nil {
			return "", err
		}

		out.WriteString(table)
	}

	return out.String(), nil
}

func renderReadmeTable(secs []*Section, h string) (string, error) {
	var out strings.Builder

	for _, sec := range secs {
		section, err := renderSection(sec, h)
		if err != nil {
			return "", err
		}

		out.WriteString("\n" + section)
	}

	return out.String(), nil
}

// insertReadmeTable replaces the tables of the README's Parameters section, keeping any text below them.
func insertReadmeTable(readmePath string, sections []*Section, cfg *Config) error {
	raw, err := os.ReadFile(readmePath)
	if err != nil {
		return fmt.Errorf("read README: %w", err)
	}

	lines := newlineRe.Split(string(raw), -1)

	start, heading, err := findParamsSection(lines, cfg)
	if err != nil {
		return err
	}

	end := tableEnd(lines, start, sectionEnd(lines, start, heading))

	// Child headings get one more '#'
	newTable, err := renderReadmeTable(sections, heading+"#")
	if err != nil {
		return err
	}

	newLines := append([]string{}, lines[:start]...)
	newLines = append(newLines, strings.Split(newTable, "\n")...)
	newLines = append(newLines, lines[end:]...)

	//nolint:gosec // READMEs are meant to be world-readable
	err = os.WriteFile(readmePath, []byte(strings.Join(newLines, "\n")), generatedFileMode)
	if err != nil {
		return fmt.Errorf("write README: %w", err)
	}

	return nil
}

// findParamsSection returns the line after the Parameters heading (##+ cfg.Regexp.ParamsSectionTitle) and its hashes.
func findParamsSection(lines []string, cfg *Config) (int, string, error) {
	titleRe, err := regexp.Compile("^(##+) " + cfg.Regexp.ParamsSectionTitle)
	if err != nil {
		return 0, "", fmt.Errorf("invalid paramsSectionTitle: %w", err)
	}

	for i, line := range lines {
		if match := titleRe.FindStringSubmatch(line); match != nil {
			return i + 1, match[1], nil
		}
	}

	return 0, "", errNoParamsSection
}

// sectionEnd returns the index of the next heading of the same level, or len(lines).
func sectionEnd(lines []string, start int, heading string) int {
	sameLevel := regexp.MustCompile("^" + heading + `\s`)

	for i := start; i < len(lines); i++ {
		if sameLevel.MatchString(lines[i]) {
			return i
		}
	}

	return len(lines)
}

// tableEnd returns where the replaced block ends: after the last table line and the blank line below it.
// Without a table, only the line right after the heading is replaced.
func tableEnd(lines []string, start, end int) int {
	for i := end - 1; i >= start; i-- {
		if lines[i] != "" && !isTextLine(lines[i]) {
			afterTable := i + 1

			return min(afterTable+1, len(lines))
		}
	}

	return min(start+1, len(lines))
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
	vals map[string]any
}

func newNode() *schemaNode {
	return &schemaNode{vals: map[string]any{}}
}

// MarshalJSON writes the keys in insertion order.
func (n *schemaNode) MarshalJSON() ([]byte, error) {
	var out strings.Builder
	out.WriteString("{")

	for i, key := range n.keys {
		if i > 0 {
			out.WriteString(",")
		}

		keyJSON, err := jsonString(key)
		if err != nil {
			return nil, err
		}

		valJSON, err := jsonString(n.vals[key])
		if err != nil {
			return nil, err
		}

		out.WriteString(keyJSON + ":" + valJSON)
	}

	out.WriteString("}")

	return []byte(out.String()), nil
}

// set adds or replaces a key and returns n; a replaced key keeps its original position, like a JS object.
func (n *schemaNode) set(key string, val any) *schemaNode {
	if _, ok := n.vals[key]; !ok {
		n.keys = append(n.keys, key)
	}

	n.vals[key] = val

	return n
}

// child returns the node stored at key, if any.
func (n *schemaNode) child(key string) (*schemaNode, bool) {
	c, ok := n.vals[key].(*schemaNode)
	return c, ok
}

// arrayKeyRe matches a path component indexing an array of objects, e.g. jobs[0]
var arrayKeyRe = regexp.MustCompile(`^(.+)\[.+\]$`)

// addSchema writes the schema for param along tree (its dot-split name) into props.
// Inside arrays of objects the item defaults are ignored.
func addSchema(param *Parameter, tree []string, props *schemaNode, ignoreDefault bool, cfg *Config) error {
	for idx, part := range tree {
		if idx == len(tree)-1 {
			props.set(part, leafSchema(param, ignoreDefault, cfg))

			return nil
		}

		if match := arrayKeyRe.FindStringSubmatch(part); match != nil {
			itemProps, err := arrayItemProps(props, match[1], param)
			if err != nil {
				return err
			}

			return addSchema(param, tree[idx+1:], itemProps, true, cfg)
		}

		next, err := objectProps(props, part, param)
		if err != nil {
			return err
		}

		props = next
	}

	return nil
}

// leafSchema returns the schema of a single value.
func leafSchema(param *Parameter, ignoreDefault bool, cfg *Config) *schemaNode {
	nullable := param.HasModifier(cfg.Modifiers.Nullable)

	var typ any = param.Type
	if nullable && param.Type != "" {
		// Helm validates with JSON Schema, which ignores OpenAPI's nullable keyword
		typ = []string{param.Type, "null"}
	}

	node := newNode().set("type", typ).set("description", param.Description)
	if !ignoreDefault {
		node.set("default", schemaDefault(param.Value))
	}

	if nullable {
		node.set("nullable", true)
	}

	if param.Type == typeArray {
		items := newNode()
		if arr, ok := param.Value.([]any); ok && len(arr) > 0 {
			items.set("type", inferType(arr[0]))
		}

		node.set("items", items)
	}

	return node
}

// arrayItemProps returns the item properties of the array of objects at key, creating the array if needed.
func arrayItemProps(props *schemaNode, key string, param *Parameter) (*schemaNode, error) {
	if _, ok := props.vals[key]; !ok {
		props.set(key, newNode().
			set("type", typeArray).
			set("description", param.Description).
			set("items", newNode().set("type", typeObject).set("properties", newNode())))
	}

	if arr, ok := props.child(key); ok {
		if items, ok := arr.child("items"); ok {
			if itemProps, ok := items.child("properties"); ok {
				return itemProps, nil
			}
		}
	}

	return nil, fmt.Errorf("%w for %s", errSchemaConflict, param.Name)
}

// objectProps returns the properties of the object at key, creating the object if needed.
func objectProps(props *schemaNode, key string, param *Parameter) (*schemaNode, error) {
	if _, ok := props.vals[key]; !ok {
		props.set(key, newNode().set("type", typeObject).set("properties", newNode()))
	}

	if obj, ok := props.child(key); ok {
		if next, ok := obj.child("properties"); ok {
			return next, nil
		}
	}

	return nil, fmt.Errorf("%w for %s", errSchemaConflict, param.Name)
}

// schemaDefault maps the "nil" and "null" placeholders to JSON null.
func schemaDefault(v any) any {
	if v == nilValue || v == "null" {
		return nil
	}

	return v
}

// checkNilValues rejects null values whose parameter isn't nullable.
func checkNilValues(params []*Parameter, cfg *Config) error {
	var nils []string

	for _, p := range params {
		if p.Value == nilValue && !p.HasModifier(cfg.Modifiers.Nullable) {
			nils = append(nils, p.Name)
		}
	}

	if len(nils) > 0 {
		return fmt.Errorf("%w: %s", errNilValues, strings.Join(nils, ", "))
	}

	return nil
}

// buildSchema returns the root schema object for params.
func buildSchema(params []*Parameter, cfg *Config) (*schemaNode, error) {
	props := newNode()
	root := newNode().set("title", "Chart Values").set("type", typeObject).set("properties", props)

	for _, param := range params {
		// Object modifier entries are README-only; params without a value only exist in the metadata
		if param.Extra() || !param.Schema || param.HasModifier(cfg.Modifiers.Object) || param.Value == nil {
			continue
		}

		err := addSchema(param, strings.Split(param.Name, "."), props, false, cfg)
		if err != nil {
			return nil, err
		}
	}

	return root, nil
}

func renderOpenAPISchema(path string, params []*Parameter, cfg *Config) error {
	err := checkNilValues(params, cfg)
	if err != nil {
		return err
	}

	root, err := buildSchema(params, cfg)
	if err != nil {
		return err
	}

	// Indent like JSON.stringify(schema, null, 4): no HTML escaping, no trailing newline
	var out strings.Builder

	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "    ")

	err = enc.Encode(root)
	if err != nil {
		return fmt.Errorf("encode schema: %w", err)
	}

	err = os.WriteFile(path, []byte(strings.TrimSuffix(out.String(), "\n")), generatedFileMode)
	if err != nil {
		return fmt.Errorf("write schema: %w", err)
	}

	return nil
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

	err = checkKeys(valuesObj, meta.Parameters)
	if err != nil {
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

		err := insertReadmeTable(opts.readmePath, meta.Sections, cfg)
		if err != nil {
			return err
		}

		fmt.Println("README updated ✅")
	}

	if opts.schemaPath != "" {
		meta.Parameters = buildParamsToRender(meta.Parameters, cfg, applySchemaModifiers)

		err := renderOpenAPISchema(opts.schemaPath, meta.Parameters, cfg)
		if err != nil {
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
	opts, err := parseFlags(os.Args[1:])

	switch {
	case errors.Is(err, flag.ErrHelp):
		fmt.Print(usage)
		os.Exit(0)
	case errors.Is(err, errInvalidFlags):
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprint(os.Stderr, "\n"+usage)
		os.Exit(2) //nolint:mnd // flag package convention for usage errors
	case err != nil:
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	err = runReadmeGenerator(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
