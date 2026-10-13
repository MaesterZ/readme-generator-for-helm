package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	flag "github.com/spf13/pflag"
)

const cliName = "readme-generator-for-helm"

// exitUsage is the exit code for invalid arguments, following the flag package convention.
const exitUsage = 2

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

Short flags take one dash (-v values.yaml), long flags two (--values values.yaml).
Single-dash long flags such as -values are no longer accepted.
`

var (
	// valueFlags take their value from the next argument unless written as --flag=value.
	valueFlags = []string{"-v", "--values", "-r", "--readme", "-c", "--config", "-s", "--schema"}
	// longFlags need two dashes: with one, pflag would read -values as -v alues.
	longFlags = []string{"values", "readme", "config", "schema", "version", "help"}
)

// parseFlags parses the command-line arguments (without the program name) and reports whether --version
// was given. It returns flag.ErrHelp when help is requested or no arguments are given.
func parseFlags(args []string) (*options, bool, error) {
	if len(args) == 0 {
		return nil, false, flag.ErrHelp
	}

	opts := &options{}

	var showVersion bool

	flags := flag.NewFlagSet(cliName, flag.ContinueOnError)
	flags.SetOutput(io.Discard) // usage is printed by execute
	flags.StringVarP(&opts.valuesPath, "values", "v", "", "")
	flags.StringVarP(&opts.readmePath, "readme", "r", "", "")
	flags.StringVarP(&opts.configPath, "config", "c", "", "")
	flags.StringVarP(&opts.schemaPath, "schema", "s", "", "")
	flags.BoolVar(&showVersion, "version", false, "")

	err := flags.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return nil, false, flag.ErrHelp
	}

	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", errInvalidFlags, err)
	}

	// Positional arguments used to be silently ignored
	if flags.NArg() > 0 {
		return nil, false, fmt.Errorf("%w: unexpected argument %q", errInvalidFlags, flags.Arg(0))
	}

	return opts, showVersion, nil
}

// run validates the options and generates the README and/or schema.
func run(opts *options) error {
	if opts.valuesPath == "" {
		return errValuesRequired
	}

	if opts.readmePath == "" && opts.schemaPath == "" {
		return errNothingToDo
	}

	// Default config path next to executable
	if opts.configPath == "" {
		exe, _ := os.Executable()
		opts.configPath = filepath.Join(filepath.Dir(exe), "config.json")
	}

	return runReadmeGenerator(opts)
}

// checkSingleDashFlags rejects long flags written with one dash (-values), which Go's flag package
// accepted but pflag would misread as a shorthand followed by a value.
func checkSingleDashFlags(args []string) error {
	expectValue := false

	for _, arg := range args {
		if expectValue {
			expectValue = false
			continue
		}

		if arg == "--" {
			return nil
		}

		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") {
			name, _, _ := strings.Cut(arg[1:], "=")
			if slices.Contains(longFlags, name) {
				return fmt.Errorf("%w: use --%s instead of -%s", errInvalidFlags, name, name)
			}
		}

		expectValue = slices.Contains(valueFlags, arg)
	}

	return nil
}

// execute runs the CLI with args (without the program name) and returns the process exit code.
func execute(args []string, stdout, stderr io.Writer) int {
	var (
		opts        *options
		showVersion bool
	)

	err := checkSingleDashFlags(args)
	if err == nil {
		opts, showVersion, err = parseFlags(args)
	}

	switch {
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(stdout, usage)

		return 0
	case err != nil:
		fmt.Fprintln(stderr, err)
		fmt.Fprint(stderr, "\n"+usage)

		return exitUsage
	case showVersion:
		fmt.Fprintln(stdout, "Version:", version)

		return 0
	}

	err = run(opts)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	return 0
}

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr))
}
