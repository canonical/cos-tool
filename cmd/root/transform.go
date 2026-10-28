package root

import (
	"fmt"
	"os"

	"github.com/canonical/cos-tool/pkg/batch"
	"github.com/canonical/cos-tool/pkg/tool"
	cli "github.com/urfave/cli/v2"
)

const (
	flagBatch        = "batch"
	flagOut          = "out"
	flagBatchVersion = "batch-version"
	flagLabelMatcher = "label-matcher"
)

// formats maps the --format and per-record format names to implementations.
// Both are resolved from here so the two paths cannot drift apart.
func formats() map[string]batch.Transformer {
	return map[string]batch.Transformer{
		"promql": &tool.PromQL{},
		"logql":  &tool.LogQL{},
	}
}

// runTransform dispatches the transform command.
//
// The order of these checks is part of the interface: --batch-version is
// answered without touching the filesystem, and a bad flag combination fails
// before any file is created.
func runTransform(c *cli.Context) error {
	if c.Bool(flagBatchVersion) {
		fmt.Fprintln(c.App.Writer, batch.Version)
		return nil
	}

	batchPath := c.String(flagBatch)
	outPath := c.String(flagOut)
	args := c.Args()

	switch {
	case batchPath != "" && outPath == "":
		return usageError("--batch requires --out")
	case outPath != "" && batchPath == "":
		return usageError("--out requires --batch")
	case batchPath != "" && args.Len() > 0:
		return usageError("--batch cannot be combined with an expression argument")
	case batchPath != "":
		return runBatchTransform(c, batchPath, outPath)
	case args.Len() != 1:
		return usageError("expected exactly one argument: the expression")
	default:
		return runSingleTransform(c, args.First())
	}
}

// runSingleTransform preserves the original single-expression behaviour:
// transform one expression and print it to stdout.
func runSingleTransform(c *cli.Context, expr string) error {
	matchers, err := tool.GetLabelMatchers(c.StringSlice(flagLabelMatcher))
	if err != nil {
		return cli.Exit(err, 1)
	}

	transformer := c.Context.Value(implKey).(tool.Checker)
	output, err := transformer.Transform(expr, &matchers)
	if err != nil {
		return err
	}

	fmt.Fprint(c.App.Writer, output)
	return nil
}

// runBatchTransform transforms a batch file into a results file.
//
// Every error path here means the batch did not complete, so the results file
// must not be trusted. Per-expression failures never reach this function:
// they are result lines.
func runBatchTransform(c *cli.Context, batchPath, outPath string) error {
	matchers, err := tool.GetLabelMatchers(c.StringSlice(flagLabelMatcher))
	if err != nil {
		return cli.Exit(err, 1)
	}

	in, err := os.Open(batchPath)
	if err != nil {
		return cli.Exit(fmt.Errorf("cannot read batch file: %w", err), 1)
	}
	defer in.Close() //nolint:errcheck // read-only; nothing to report on close

	// Truncates an existing file, as documented. Opened only after the flag
	// combination is known to be valid, so a usage error creates nothing.
	out, err := os.Create(outPath)
	if err != nil {
		return cli.Exit(fmt.Errorf("cannot write results file: %w", err), 1)
	}

	runner := batch.NewRunner(batch.Options{
		DefaultFormat:   canonicalFormat(c.String("format")),
		DefaultMatchers: matchers,
		Diagnostics:     c.App.ErrWriter,
	}, batch.NewMapResolver(formats()))

	runErr := runner.Run(in, out)

	// Close explicitly: on many filesystems a write error only surfaces here,
	// and silently dropping it would turn a short results file into a
	// successful invocation.
	closeErr := out.Close()

	if runErr != nil {
		return cli.Exit(runErr, 1)
	}
	if closeErr != nil {
		return cli.Exit(fmt.Errorf("writing results file: %w", closeErr), 1)
	}

	return nil
}

// usageError reports a misuse of the command's flags. It exits non-zero
// without having created any file.
func usageError(message string) error {
	return cli.Exit(fmt.Errorf("%s", message), 1)
}
