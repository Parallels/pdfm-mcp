package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRealRunnerCapturesStdout(t *testing.T) {
	r := NewCommandRunner()
	out, err := r.Run(context.Background(), "/bin/echo", "hello", "world")
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello world\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestRealRunnerMissingBinary(t *testing.T) {
	r := NewCommandRunner()
	_, err := r.Run(context.Background(), "/definitely/not/here")
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestRealRunnerStderrFoldedIntoError(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fail.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho oops 1>&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := NewCommandRunner()
	_, err := r.Run(context.Background(), "/bin/sh", script)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "oops") {
		t.Fatalf("stderr not folded into error: %v", err)
	}
}

func TestRealRunnerStderrIsTruncated(t *testing.T) {
	dir := t.TempDir()
	// Emit 2000 bytes of stderr; the error must carry at most 500.
	script := filepath.Join(dir, "loud.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 2000 ]; do echo x; i=$((i+1)); done 1>&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := NewCommandRunner()
	_, err := r.Run(context.Background(), "/bin/sh", script)
	if err == nil {
		t.Fatal("want error")
	}
	if len(err.Error()) > 700 {
		t.Fatalf("stderr not truncated: %d bytes", len(err.Error()))
	}
}

func TestRealRunnerTimeout(t *testing.T) {
	r := NewCommandRunner()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := r.Run(ctx, "/bin/sleep", "5")
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("err = %v", err)
	}
}

func TestRealRunnerDefaultCapWhenNoCallerDeadline(t *testing.T) {
	orig := defaultCommandTimeout
	defaultCommandTimeout = 150 * time.Millisecond
	t.Cleanup(func() { defaultCommandTimeout = orig })

	r := NewCommandRunner()
	_, err := r.Run(context.Background(), "/bin/sleep", "5")
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("err = %v, want default-cap timeout", err)
	}
}

func TestRealRunnerLongCallerDeadlineWins(t *testing.T) {
	// Power operations pass a long deadline; it must not be clamped by the
	// default cap. A 3s caller deadline must let a 1.5s command finish.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	r := NewCommandRunner()
	start := time.Now()
	_, err := r.Run(ctx, "/bin/sleep", "1.5")
	if err != nil {
		t.Fatalf("err = %v, want success within the caller deadline", err)
	}
	if time.Since(start) < 1200*time.Millisecond {
		t.Fatalf("finished suspiciously fast (%s), deadline may have been clamped", time.Since(start))
	}
}

func TestRealRunnerOutputCap(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(big, make([]byte, maxOutputBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}

	r := NewCommandRunner()
	_, err := r.Run(context.Background(), "/bin/cat", big)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want output cap error", err)
	}
}
