package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func run(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = Run(context.Background(), args, IO{
		Stdin:  strings.NewReader(""),
		Stdout: &out,
		Stderr: &errOut,
	}, Env{Vars: map[string]string{}})
	return code, out.String(), errOut.String()
}

func TestRunVersion(t *testing.T) {
	code, stdout, stderr := run("--version")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr)
	}
	if want := "notesteme dev\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestRunNotImplemented(t *testing.T) {
	code, stdout, stderr := run("serve")
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "not implemented") {
		t.Errorf("stderr = %q, want it to contain %q", stderr, "not implemented")
	}
}

func TestRunNoArgs(t *testing.T) {
	code, _, stderr := run()
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if stderr == "" {
		t.Error("stderr is empty, want usage")
	}
}
