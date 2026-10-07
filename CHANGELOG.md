# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Brings the Go rewrite to parity with the Node.js
[readme-generator-for-helm](https://github.com/bitnami/readme-generator-for-helm) on its own test fixtures:
README output is now byte-identical, and the schema is identical except for the intentional
differences listed under **Changed**.

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
- `<`, `>` and `&` are no longer escaped as `<` etc. in the schema and README values.
- The schema honours custom `object` and `nullable` modifier names from the config file.
- README modifiers no longer leak into the schema, since both are now built from separate copies
  of the parameters.

### Changed

- The schema `default` for parameters with the `array` or `string` modifier is the real value from
  `values.yaml`; the README still shows the `[]` / `""` placeholder. The Node.js version writes
  the placeholders as strings (`"[]"`, `"\"\""`) and derives `items.type` from them, which makes
  Helm reject an array of objects. `[default: VALUE]` still overrides both.
- Nullable parameters get `"type": ["<type>", "null"]` in the schema. Helm validates
  `values.schema.json` as JSON Schema, which ignores OpenAPI's `nullable` keyword, so `null` values
  previously failed `helm lint`. `nullable: true` is still emitted for OpenAPI consumers.

## [1.0.1] - 2026-03-30

### Fixed

- Missing blank line after a section description.

## [1.0.0] - 2025-07-17

### Added

- Initial Go rewrite of the Node.js readme-generator-for-helm, with the same command-line interface.

[Unreleased]: https://github.com/cozystack/readme-generator-for-helm/compare/v1.0.1...HEAD
[1.0.1]: https://github.com/cozystack/readme-generator-for-helm/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/cozystack/readme-generator-for-helm/releases/tag/v1.0.0
