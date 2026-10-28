package root_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var (
	buildOnce sync.Once
	buildPath string
	buildErr  error
)

// buildBinary compiles cos-tool once per test run and returns its path.
//
// The batch contract is largely about exit status and about which files exist
// afterwards, neither of which can be observed by calling the action
// in-process: urfave/cli's exit handling calls os.Exit.
//
// Takes testing.TB so that benchmarks, which must measure real process
// spawns, can share the single build.
func buildBinary(t testing.TB) string {
	t.Helper()

	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cos-tool-clitest")
		if err != nil {
			buildErr = err
			return
		}

		buildPath = filepath.Join(dir, "cos-tool")

		root, err := filepath.Abs("../..")
		if err != nil {
			buildErr = err
			return
		}

		cmd := exec.Command("go", "build", "-o", buildPath, ".")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = errors.New("building cos-tool: " + err.Error() + "\n" + string(out))
		}
	})

	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return buildPath
}

// runTool invokes the binary and returns its stdout, stderr and exit code.
func runTool(t testing.TB, bin string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()

	var outBuf, errBuf bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		exitCode = 0
	case errors.As(err, &exitErr):
		exitCode = exitErr.ExitCode()
	default:
		t.Fatalf("running %s: %v", bin, err)
	}

	return outBuf.String(), errBuf.String(), exitCode
}
