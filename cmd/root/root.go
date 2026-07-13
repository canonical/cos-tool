package root

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/canonical/cos-tool/pkg/tool"
	cli "github.com/urfave/cli/v2"
)

// Define a private, unique key type
type contextKey string

const implKey contextKey = "impl"

var app = &cli.App{
	Name:            "cos-tool",
	Usage:           "Validates Prometheus and Loki expressions, adds Juju Topology to label matchers",
	HideHelpCommand: false,
	HideHelp:        false,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "format",
			Aliases: []string{"f"},
			Value:   "promql",
			Usage:   "Inject expressions into `promql|logql`",
		},
	},
	Commands: []*cli.Command{
		{
			Name:      "transform",
			Aliases:   []string{"t"},
			Usage:     "Inject Juju topology label matchers into an expression",
			ArgsUsage: "<expression>",
			Description: `Rewrite a single PromQL or LogQL expression, injecting the given label
matchers into every vector/log-stream selector. Use the global --format flag
to select the expression language.

EXAMPLES:
   cos-tool --format promql transform \
       --label-matcher juju_model=cos \
       -- 'rate(http_requests_total{job="myjob"}[5m]) > 0.5'`,
			Flags: []cli.Flag{
				&cli.StringSliceFlag{
					Name:  "label-matcher",
					Usage: "Label matcher to inject into all vector selectors",
				},
			},
			Action: func(c *cli.Context) error {
				args := c.Args()

				if args.Len() != 1 {
					log.Fatal("Expected exactly one argument: the expression.")
				}

				inj, err := tool.GetLabelMatchers(c.StringSlice("label-matcher"))
				if err != nil {
					log.Fatal(err)
				}

				transformer := c.Context.Value(implKey).(tool.Checker)
				output, err := transformer.Transform(args.First(), &inj)
				if err != nil {
					return err
				}

				fmt.Print(output)
				return nil
			},
		},
		{
			Name:      "validate-rules",
			Aliases:   []string{"v", "lint", "l", "validate"},
			Usage:     "Validate Prometheus/Loki alert rules",
			ArgsUsage: "<rule_file.yaml> [rule_file.yaml ...]",
			Description: `Validate that alert rules can be loaded successfully by Prometheus (PromQL)
or Loki (LogQL). Use the global --format flag to select the rule language.

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
		me := strings.ToLower(c.String("format"))
		switch me {
		case "promql":
			c.Context = context.WithValue(c.Context, implKey, &tool.PromQL{})
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
