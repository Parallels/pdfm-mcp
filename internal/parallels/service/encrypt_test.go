package service

import (
	"context"
	"strings"
	"testing"
)

func TestEncryptArgvAndStdin(t *testing.T) {
	runner := &fakeRunner{fn: func(_ string, _ []string) (string, error) {
		return "  The virtual machine has been successfully encrypted.\n", nil
	}}
	svc := powerService(t, runner, "")

	out, err := svc.Encrypt(context.Background(), "guest", "s3cret-pw", false)
	if err != nil {
		t.Fatal(err)
	}
	if out != "The virtual machine has been successfully encrypted." {
		t.Fatalf("out = %q", out)
	}
	if len(runner.lastArgs) != 2 || runner.lastArgs[0] != "encrypt" || runner.lastArgs[1] != "guest" {
		t.Fatalf("argv = %v", runner.lastArgs)
	}
	if runner.lastIn != "s3cret-pw\n" {
		t.Fatalf("stdin = %q, want the single password line", runner.lastIn)
	}
}

func TestEncryptDryRunUsesRunWithoutStdin(t *testing.T) {
	runner := &fakeRunner{fn: func(_ string, _ []string) (string, error) {
		return "All prerequisites for the virtual machine encryption have been met", nil
	}}
	svc := powerService(t, runner, "")

	// Dry-run: no password needed and none may be fed to the child.
	out, err := svc.Encrypt(context.Background(), "guest", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Fatal("empty dry-run output")
	}
	if len(runner.lastArgs) != 3 || runner.lastArgs[0] != "encrypt" || runner.lastArgs[1] != "guest" || runner.lastArgs[2] != "--dry-run" {
		t.Fatalf("argv = %v", runner.lastArgs)
	}
	if runner.lastIn != "" {
		t.Fatalf("dry-run must not feed stdin, got %q", runner.lastIn)
	}
}

func TestDecryptArgvAndStdin(t *testing.T) {
	runner := &fakeRunner{fn: func(_ string, _ []string) (string, error) {
		return "The virtual machine has been successfully decrypted.", nil
	}}
	svc := powerService(t, runner, "")

	if _, err := svc.Decrypt(context.Background(), "guest", "pw-1", false); err != nil {
		t.Fatal(err)
	}
	if len(runner.lastArgs) != 2 || runner.lastArgs[0] != "decrypt" || runner.lastArgs[1] != "guest" {
		t.Fatalf("argv = %v", runner.lastArgs)
	}
	if runner.lastIn != "pw-1\n" {
		t.Fatalf("stdin = %q", runner.lastIn)
	}
}

func TestChangePasswdArgvAndStdin(t *testing.T) {
	runner := &fakeRunner{fn: func(_ string, _ []string) (string, error) {
		return "The password has been successfully changed.", nil
	}}
	svc := powerService(t, runner, "")

	out, err := svc.ChangePasswd(context.Background(), "guest", "old-pw", "new-pw")
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Fatal("empty output")
	}
	if len(runner.lastArgs) != 2 || runner.lastArgs[0] != "change-passwd" || runner.lastArgs[1] != "guest" {
		t.Fatalf("argv = %v", runner.lastArgs)
	}
	// Two lines: current, then new — no confirmation prompt.
	if runner.lastIn != "old-pw\nnew-pw\n" {
		t.Fatalf("stdin = %q, want current+new lines", runner.lastIn)
	}
}

func TestEncryptionPasswordValidation(t *testing.T) {
	cases := []struct {
		name string
		pw   string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"non-printable", "pw\x00bad"},
		{"too long", strings.Repeat("a", MaxPasswordLength+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{}
			svc := powerService(t, runner, "")
			if _, err := svc.Encrypt(context.Background(), "guest", tc.pw, false); err == nil {
				t.Fatal("want validation error")
			}
			if runner.calls != 0 {
				t.Fatal("prlctl must not run for an invalid password")
			}
			// change-passwd validates both slots
			runner2 := &fakeRunner{}
			svc2 := powerService(t, runner2, "")
			if _, err := svc2.ChangePasswd(context.Background(), "guest", "good", tc.pw); err == nil {
				t.Fatal("want validation error for new password")
			}
			if runner2.calls != 0 {
				t.Fatal("prlctl must not run for an invalid new password")
			}
		})
	}
}

func TestEncryptionNonZeroExitKeepsPasswordOut(t *testing.T) {
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return "", "Failed to authorise current session. The password you specified is invalid.", 255
	}}
	svc := powerService(t, runner, "")

	_, err := svc.Decrypt(context.Background(), "guest", "pw-zx9-invalid", false)
	if err == nil {
		t.Fatal("want error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "password you specified is invalid") {
		t.Fatalf("error lost prlctl's message: %v", err)
	}
	if strings.Contains(err.Error(), "pw-zx9-invalid") {
		t.Fatalf("password leaked into the error: %v", err)
	}
}

func TestEncryptionInvalidatesCache(t *testing.T) {
	var listRuns int
	runner := &fakeRunner{fn: func(_ string, args []string) (string, error) {
		switch args[0] {
		case "list":
			listRuns++
			return "[]", nil
		case "encrypt", "change-passwd":
			return "ok", nil
		}
		return "", nil
	}}
	svc := powerService(t, runner, "")
	ctx := context.Background()

	if _, err := svc.ListVMs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListVMs(ctx, false); err != nil { // cache hit
		t.Fatal(err)
	}
	if listRuns != 1 {
		t.Fatalf("list runs before encrypt = %d, want 1", listRuns)
	}

	// A real run flips Security.Encrypted: the next read must re-fetch.
	if _, err := svc.Encrypt(ctx, "guest", "pw", false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListVMs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if listRuns != 2 {
		t.Fatalf("list runs after real encrypt = %d, want 2", listRuns)
	}

	// A dry-run changes nothing: no extra re-fetch.
	if _, err := svc.ListVMs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Encrypt(ctx, "guest", "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListVMs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if listRuns != 2 {
		t.Fatalf("list runs after dry-run = %d, want 2 (dry-run must not invalidate)", listRuns)
	}

	// change-passwd invalidates.
	if _, err := svc.ChangePasswd(ctx, "guest", "a", "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListVMs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if listRuns != 3 {
		t.Fatalf("list runs after change-passwd = %d, want 3", listRuns)
	}
}
