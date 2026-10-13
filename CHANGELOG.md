# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

First release of the fork. The Go rewrite reaches parity with the Node.js
[readme-generator-for-helm](https://github.com/bitnami/readme-generator-for-helm) on its own test fixtures
(README output is byte-identical; the schema differs only as listed under **Changed**), produces schemas
that Helm 3 and 4 accept, and passes golangci-lint with no issues.

### Added

- Running without arguments prints the help and exits 0, as `-h` / `--help` do. Invalid arguments
  print the error and the usage to stderr and exit 2.
- golangci-lint configuration (`.golangci.yml`): all linters enabled, minus a short disable list.
- `Makefile` with `build`, `release`, `test`, `lint`, `format`, `tidy` and `clean` targets, and a
  `.gitignore` for the binary and editor files.
- Golden tests using the Node.js README fixtures, a schema golden file, and unit tests for each fix.

### Fixed

- Arrays containing only strings (e.g. `args: [--foo, --bar]`) are documented as a single key
  (`args`) instead of failing validation with `Missing metadata for key: args[0]`.
- Text after the parameter tables in the README's Parameters section is preserved; previously the
  whole section was replaced up to the next heading. A section without a table keeps all its text.
- READMEs with CRLF line endings are normalised to LF, as in the Node.js version.
- Arrays of objects (e.g. `jobs[0].name`) are rendered in the schema as `type: array` with `items`,
  instead of an object property literally named `"jobs[0]"`. Item defaults are omitted.
- Keys containing dots (e.g. `prometheus.io/scrape`) are left out of the schema instead of being
  split into a wrong nested tree.
- `null` values are shown as `nil` in the README, and schema generation fails with
  `invalid type 'nil'` unless the parameter has the `nullable` modifier.
- Nullable `null` values get `"default": null` in the schema instead of the string `"nil"`, and
  `type: object` instead of the invalid `type: nil`.
- Schema keys follow the order of the `values.yaml` metadata (and `type`, `description`,
  `default`, ... within each entry) instead of being sorted alphabetically.
- `<`, `>` and `&` are no longer escaped as `\u003c` etc. in the schema and README values.
- The schema honours custom `object` and `nullable` modifier names from the config file.
- README modifiers no longer leak into the schema, since both are now built from separate copies
  of the parameters.
- Values that JSON can't represent (YAML `.inf`, `.nan`) fail with an error instead of silently
  rendering an empty README cell.
- An invalid `paramsSectionTitle` regexp in the config file is reported as an error instead of
  crashing.

### Changed

- The project now lives at [MaesterZ/readme-generator-for-helm](https://github.com/MaesterZ/readme-generator-for-helm):
  Cozystack stopped using this generator in [cozystack#1218](https://github.com/cozystack/cozystack/pull/1218)
  and the upstream no longer takes changes. The Go module path is now
  `github.com/MaesterZ/readme-generator-for-helm`, so install with
  `go install github.com/MaesterZ/readme-generator-for-helm@latest`.
- Require Go 1.27 and switch from the archived `gopkg.in/yaml.v3` to its maintained successor
  `go.yaml.in/yaml/v3` (v3.0.5, same API).
- The schema `default` for parameters with the `array` or `string` modifier is the real value from
  `values.yaml`; the README still shows the `[]` / `""` placeholder. The Node.js version writes
  the placeholders as strings (`"[]"`, `"\"\""`) and derives `items.type` from them, which makes
  Helm reject an array of objects. `[default: VALUE]` still overrides both.
- Nullable parameters get `"type": ["<type>", "null"]` in the schema. Helm validates
  `values.schema.json` as JSON Schema, which ignores OpenAPI's `nullable` keyword, so `null` values
  previously failed `helm lint`. `nullable: true` is still emitted for OpenAPI consumers.
- Flags are parsed with [pflag](https://github.com/spf13/pflag), GNU-style as in the Node.js version.
  Long flags need two dashes: `-values` is rejected with `use --values instead of -values` (Go's
  flag package used to accept it). Positional arguments, previously ignored, are rejected.
- Error messages say which step failed, e.g. `read values: open values.yaml: no such file or directory`.
- Fixed all 294 golangci-lint findings, with no change to the generated README or schema:
  - errors are named or wrapped with context, and JSON encoding errors are no longer ignored;
  - the long parsing, rendering and schema functions are split into small helpers, and the
    README and schema modifier logic is shared instead of duplicated;
  - `io/ioutil` is replaced by `os`, and `interface{}` by `any`;
  - tests run in parallel (`t.Parallel()`) and pass with `-race`.

## [1.0.1] - 2026-03-30

### Fixed

- Missing blank line after a section description.

## [1.0.0] - 2025-07-17

### Added

- Initial Go rewrite of the Node.js readme-generator-for-helm, with the same command-line interface.

[Unreleased]: https://github.com/MaesterZ/readme-generator-for-helm/compare/v1.0.1...HEAD
[1.0.1]: https://github.com/MaesterZ/readme-generator-for-helm/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/MaesterZ/readme-generator-for-helm/releases/tag/v1.0.0
