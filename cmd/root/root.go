package root

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/canonical/cos-tool/pkg/tool"
	cli "github.com/urfave/cli/v2"
)

// Define a private, unique key type
type contextKey string

const implKey contextKey = "impl"

const defaultFormat = "promql"

// canonicalFormat normalizes the global --format value.
//
// Unknown values have always fallen back to PromQL rather than being
// rejected, and that is preserved here. It also matters for batch mode: the
// global format is the default for every record that does not name one, so
// rejecting it lazily would turn one bad flag into 50,000 malformed records.
// Per-record format values are a new interface and are validated strictly.
func canonicalFormat(format string) string {
	switch strings.ToLower(format) {
	case "promql", "logql":
		return strings.ToLower(format)
	default:
		return defaultFormat
	}
}

var app = &cli.App{
	Name:            "cos-tool",
	Usage:           "Validates Prometheus and Loki expressions, adds Juju Topology to label matchers",
	HideHelpCommand: false,
	HideHelp:        false,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "format",
			Aliases: []string{"f"},
			Value:   defaultFormat,
			Usage:   "Inject expressions into `promql|logql`",
		},
	},
	Commands: []*cli.Command{
		{
			Name:      "transform",
			Aliases:   []string{"t"},
			Usage:     "Inject Juju topology label matchers into an expression",
			ArgsUsage: "<expression>",
			Description: `Rewrite a PromQL or LogQL expression, injecting the given label matchers
into every vector/log-stream selector. Use the global --format flag to select
the expression language.

One expression per invocation is transformed by passing it as an argument. To
transform many expressions at once, pass --batch and --out: cos-tool then reads
one JSON request per line from the batch file and writes one JSON result per
line to the results file, in input order.

The exit status reports only whether cos-tool completed the batch, not whether
every expression was valid; inspect the "status" field of each result.

EXAMPLES:
   cos-tool --format promql transform \
       --label-matcher juju_model=cos \
       -- 'rate(http_requests_total{job="myjob"}[5m]) > 0.5'

   cos-tool --format promql transform \
       --batch requests.jsonl --out results.jsonl \
       --label-matcher juju_model=cos

   cos-tool transform --batch-version`,
			Flags: []cli.Flag{
				&cli.StringSliceFlag{
					Name:  flagLabelMatcher,
					Usage: "Label matcher to inject into all vector selectors. In batch mode, a batch-wide default that records extend",
				},
				&cli.StringFlag{
					Name:  flagBatch,
					Usage: "Read JSONL transform requests from `FILE` instead of an argument. Requires --out",
				},
				&cli.StringFlag{
					Name:  flagOut,
					Usage: "Write JSONL transform results to `FILE`, overwriting it. Requires --batch",
				},
				&cli.BoolFlag{
					Name:  flagBatchVersion,
					Usage: "Print the highest supported batch interface version and exit",
				},
			},
			Action: runTransform,
		},
		{
			Name:      "validate-rules",
			Aliases:   []string{"v", "lint", "l", "validate"},
			Usage:     "Validate Prometheus/Loki alert rules",
			ArgsUsage: "<rule_file.yaml> [rule_file.yaml ...]",
			Description: `Validate that alert rules are valid according to the Prometheus (PromQL) or Loki (LogQL) rule formats. Use the global --format flag to select the rule language.

Rules are provided as one or more file paths.

On success there is no output and the exit code is zero. On failure, the
validation errors are printed to stderr and the exit code is non-zero.

EXAMPLES:
   # Validate one or more rule files
   cos-tool validate-rules rules.yaml more-rules.yaml

   # Validate Loki (LogQL) rules
   cos-tool --format logql validate-rules loki-rules.yaml`,
			Action: func(c *cli.Context) error {
				args := c.Args()

				if args.Len() < 1 {
					log.Fatal("Expected at least one rule file to validate.")
				}

				validator := c.Context.Value(implKey).(tool.Checker)

				for _, f := range args.Slice() {
					data, err := os.ReadFile(f)
					if err != nil {
						return err
					}

					_, err = validator.ValidateRules(f, data)
					if err != nil {
						return cli.Exit(err, 1)
					}
				}

				return nil
			},
		},
		{
			Name:      "validate-config",
			Usage:     "Validate a Prometheus/Loki configuration file",
			ArgsUsage: "<config_file.yaml> [config_file.yaml ...]",
			Description: `Validate one or more Prometheus (PromQL) or Loki (LogQL) configuration
files. Use the global --format flag to select the config type.`,
			Action: func(c *cli.Context) error {
				args := c.Args()

				if args.Len() < 1 {
					log.Fatal("Expected at least one config file to validate.")
				}

				validator := c.Context.Value(implKey).(tool.Checker)

				for _, f := range args.Slice() {
					err := validator.ValidateConfig(f)
					if err != nil {
						return err
					}
				}

				return nil
			},
		},
	},
	Before: func(c *cli.Context) error {
		switch canonicalFormat(c.String("format")) {
		case "logql":
			c.Context = context.WithValue(c.Context, implKey, &tool.LogQL{})
		default:
			c.Context = context.WithValue(c.Context, implKey, &tool.PromQL{})
		}

		return nil
	},
}

func Execute() error {
	return app.Run(os.Args)
}
