package root

import (
	"os"
	"strings"
	"testing"

	"github.com/canonical/cos-tool/pkg/tool"
	cli "github.com/urfave/cli/v2"
)

const (
	validRules = `groups:
  - name: example
    rules:
      - alert: HighErrorRate
        expr: rate(errors_total[5m]) > 1
`
	invalidRules = `groups:
  - name: example
    rules:
      - alert: HighErrorRate
        expr: rate(errors_total[5m]) >
`
)

func TestValidateStream(t *testing.T) {
	tests := []struct {
		name      string
		validator tool.Checker
		input     string
		wantErr   bool
	}{
		{"valid promql rules", &tool.PromQL{}, validRules, false},
		{"invalid promql rules", &tool.PromQL{}, invalidRules, true},
		{"valid logql rules", &tool.LogQL{}, "groups:\n  - name: example\n    rules:\n      - alert: Errors\n        expr: count_over_time({app=\"foo\"} |= \"error\" [5m]) > 1\n", false},
		{"invalid logql rules", &tool.LogQL{}, "groups:\n  - name: example\n    rules:\n      - alert: Errors\n        expr: count_over_time({app=\"foo\"} [5m]) >\n", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateStream(test.validator, "stdin", strings.NewReader(test.input))
			if (err != nil) != test.wantErr {
				t.Fatalf("validateStream() error = %v, wantErr %v", err, test.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "error validating stdin") {
				t.Fatalf("error does not name the input: %v", err)
			}
		})
	}
}

func TestStdinIsNotTerminalForNullDevice(t *testing.T) {
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open null device: %v", err)
	}

	origStdin := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() {
		os.Stdin = origStdin
		_ = stdin.Close()
	})

	if stdinIsTerminal() {
		t.Fatal("stdinIsTerminal() = true for the null device, want false")
	}
}

func TestValidateRulesReadsStdin(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantExit int
	}{
		{"valid rules", validRules, 0},
		{"invalid rules", invalidRules, 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("pipe: %v", err)
			}
			if _, err := w.WriteString(test.input); err != nil {
				t.Fatalf("write: %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}

			origStdin, origExiter, origErrWriter := os.Stdin, cli.OsExiter, app.ErrWriter
			t.Cleanup(func() {
				os.Stdin, cli.OsExiter, app.ErrWriter = origStdin, origExiter, origErrWriter
			})

			exit := 0
			os.Stdin = r
			cli.OsExiter = func(code int) { exit = code }
			app.ErrWriter = &strings.Builder{}

			err = app.Run([]string{"cos-tool", "validate-rules"})
			if (err != nil) != (test.wantExit != 0) {
				t.Fatalf("app.Run() error = %v, want exit code %d", err, test.wantExit)
			}
			if exit != test.wantExit {
				t.Fatalf("exit code = %d, want %d", exit, test.wantExit)
			}
		})
	}
}
