# Example

## Parameters

### Edge cases

| Name           | Description                                          | Value                          |
| -------------- | ---------------------------------------------------- | ------------------------------ |
| `pipe`         | Description with | pipe and `code` and ünïcödé 日本 🎉 | `a|b`                          |
| `multiline`    | Folded string                                        | `line one line two
`           |
| `literal`      | Literal string                                       | `first
second
`                |
| `anchors.base` | Anchor base                                          | `hello`                        |
| `anchors.ref`  | Alias                                                | `hello`                        |
| `date`         | Unquoted date                                        | `2001-12-14`                   |
| `octal`        | Octal-ish                                            | `15`                           |
| `hex`          | Hex                                                  | `31`                           |
| `bigint`       | Big int                                              | `1.2345678901234568e+29`       |
| `float`        | Float                                                | `1000`                         |
| `yesno`        | YAML 1.1 bool                                        | `yes`                          |
| `tilde`        | Null tilde                                           | `nil`                          |
| `emptystr`     | Empty string                                         | `""`                           |
| `emptylist`    | Empty list                                           | `[]`                           |
| `emptymap`     | Empty map                                            | `{}`                           |
| `strs`         | Only strings                                         | `["--foo","--bar"]`            |
| `objs[0].name` | Object array                                         | `a`                            |
| `timestamp`    | Timestamp                                            | `2001-12-14t21:59:43.10-05:00` |
| `tagged`       | Explicit str tag                                     | `123`                          |
| `negzero`      | Negative zero                                        | `0`                            |
| `inf`          | Leading dot                                          | `0.5`                          |
