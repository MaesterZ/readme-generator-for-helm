package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

const cliName = "readme-generator-for-helm"

// exitUsage is the exit code for invalid arguments, following the flag package convention.
const exitUsage = 2

var (
	// valueFlags take their value from the next argument unless written as --flag=value.
	valueFlags = []string{"-v", "--values", "-r", "--readme", "-c", "--config", "-s", "--schema"}
	// longFlags need two dashes: with one, pflag would read -values as -v alues.
	longFlags = []string{"values", "readme", "config", "schema", "version", "help"}
)

func newRootCmd() *cobra.Command {
	opts := &options{}

	cmd := &cobra.Command{
		Use:   cliName,
		Short: "Generate the Parameters table of a Helm chart README and its values schema",
		Long: `Generate the Parameters table of a Helm chart README and its OpenAPI values schema
from the @param metadata comments in values.yaml.

At least one of --readme or --schema must be provided.

Short flags take one dash (-v values.yaml), long flags two (--values values.yaml).
Single-dash long flags such as -values are no longer accepted.`,
		Example:       "  " + cliName + " -v values.yaml -r README.md -s values.schema.json",
		Version:       version,
		Args:          noArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}

			return run(opts)
		},
	}

	cmd.SetVersionTemplate("Version: {{.Version}}\n")
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return fmt.Errorf("%w: %w", errInvalidFlags, err)
	})

	flags := cmd.Flags()
	flags.StringVarP(&opts.valuesPath, "values", "v", "", "path to the values.yaml file (required)")
	flags.StringVarP(&opts.readmePath, "readme", "r", "", "path to the README.md file to update")
	flags.StringVarP(&opts.configPath, "config", "c", "", "path to config.json (built-in defaults if omitted)")
	flags.StringVarP(&opts.schemaPath, "schema", "s", "", "path for the generated OpenAPI schema")

	// Shell completion only suggests files with these extensions
	_ = cmd.MarkFlagFilename("values", "yaml", "yml")
	_ = cmd.MarkFlagFilename("readme", "md")
	_ = cmd.MarkFlagFilename("config", "json")
	_ = cmd.MarkFlagFilename("schema", "json")

	return cmd
}

// noArgs rejects positional arguments, which were silently ignored before.
func noArgs(_ *cobra.Command, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%w: unexpected argument %q", errInvalidFlags, args[0])
	}

	return nil
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
	if args == nil {
		args = []string{} // cobra falls back to os.Args on nil
	}

	cmd := newRootCmd()
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	err := checkSingleDashFlags(args)
	if err == nil {
		err = cmd.Execute()
	}

	switch {
	case err == nil:
		return 0
	case errors.Is(err, errInvalidFlags):
		fmt.Fprintln(stderr, err)
		fmt.Fprint(stderr, "\n"+cmd.UsageString())

		return exitUsage
	default:
		fmt.Fprintln(stderr, err)

		return 1
	}
}

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr))
}
