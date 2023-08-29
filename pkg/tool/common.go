package tool

import (
	"errors"
	logqlparser "github.com/canonical/cos-tool/pkg/logql/syntax"
	"github.com/prometheus/prometheus/model/rulefmt"
	"github.com/prometheus/prometheus/promql/parser"
	"strings"
)

type AlertRuleFile struct {
	// Namespace field only exists for setting namespace in namespace body instead of file name
	Filepath string              `yaml:"-"`
	Groups   []rulefmt.RuleGroup `yaml:"groups"`
}

type PromQL struct {
	expr     parser.Expr
	matchers *map[string]string
}

type LogQL struct {
	expr           logqlparser.Expr
	matchers       *map[string]string
	sortedMatchers *[]string
}

type Checker interface {
	Transform(arg string, matchers *map[string]string) (string, error)
	ValidateRules(filename string, data []byte) (*rulefmt.RuleGroups, error)
	ValidateConfig(filename string) error
}

func GetLabelMatchers(flags []string) (map[string]string, error) {
	inj := map[string]string{}
	for _, matcher := range flags {
		parts := strings.Split(matcher, "=")
		if len(parts) != 2 {
			return nil, errors.New("malformed label injector")
		}
		inj[parts[0]] = parts[1]
	}
	return inj, nil
}
