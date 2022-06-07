package root

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/canonical/cos-tool/pkg/tool"
	"github.com/urfave/cli/v2"
)

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
			Name:    "transform",
			Aliases: []string{"t"},
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

				transformer := c.Context.Value("impl").(tool.Checker)
				output, err := transformer.Transform(args.First(), &inj)
				if err != nil {
					return err
				}

				fmt.Print(output)
				return nil
			},
		},
		{
			Name:    "validate",
			Aliases: []string{"v", "lint", "l"},
			Action: func(c *cli.Context) error {
				args := c.Args()

				if args.Len() < 1 {
					log.Fatal("Expected at least one rule file to validate.")
				}

				validator := c.Context.Value("impl").(tool.Checker)

				for _, f := range args.Slice() {
					data, err := ioutil.ReadFile(f)
					if err != nil {
						return err
					}

					_, err = validator.Validate(data)
					if err != nil {
						return cli.Exit(err, 1)
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
			c.Context = context.WithValue(c.Context, "impl", &tool.PromQL{})
		case "logql":
			c.Context = context.WithValue(c.Context, "impl", &tool.LogQL{})
		default:
			c.Context = context.WithValue(c.Context, "impl", &tool.PromQL{})
		}

		return nil
	},
}

func Execute() error {
	return app.Run(os.Args)
}
