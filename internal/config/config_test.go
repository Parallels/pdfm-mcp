package config

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VMID != "" {
		t.Fatalf("VMID = %q, want empty", cfg.VMID)
	}
	if cfg.VMCacheTTL != DefaultVMCacheTTL {
		t.Fatalf("VMCacheTTL = %s, want %s", cfg.VMCacheTTL, DefaultVMCacheTTL)
	}
}

func TestLoadVMIDFromFlagAndEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("PDFM_MCP_VM_ID", "env-vm")

	cfg, err := Load([]string{"--vm-id", "flag-vm"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VMID != "flag-vm" {
		t.Fatalf("VMID = %q, want flag-vm (flag wins)", cfg.VMID)
	}

	cfg, err = Load(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VMID != "env-vm" {
		t.Fatalf("VMID = %q, want env-vm", cfg.VMID)
	}
}

func TestLoadVMIDTrimsWhitespace(t *testing.T) {
	clearEnv(t)
	cfg, err := Load([]string{"--vm-id", "  my vm  "}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VMID != "my vm" {
		t.Fatalf("VMID = %q, want %q", cfg.VMID, "my vm")
	}
}

func TestLoadVMIDValidation(t *testing.T) {
	clearEnv(t)
	for _, ok := range []string{"90f90e2b-e19f-4429-86b3-b5146bc860d4", "ubuntu_github_builder", ""} {
		if err := validateVMID(ok); err != nil {
			t.Fatalf("validateVMID(%q) = %v, want nil", ok, err)
		}
	}
	// parallels.MaxVMRefLength is 256; the bound now lives in the parallels
	// service (single source of truth), so build the over-long value here.
	long := make([]byte, 257)
	for i := range long {
		long[i] = 'a'
	}
	if err := validateVMID(string(long)); err == nil {
		t.Fatal("want error for over-long id")
	}
	if err := validateVMID("bad\x00id"); err == nil {
		t.Fatal("want error for NUL byte")
	}
	if err := validateVMID("bad\nid"); err == nil {
		t.Fatal("want error for control char")
	}
}

func TestLoadVMCacheTTL(t *testing.T) {
	clearEnv(t)

	t.Setenv("PDFM_MCP_VM_CACHE_TTL", "5s")
	cfg, err := Load(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VMCacheTTL != 5*time.Second {
		t.Fatalf("VMCacheTTL = %s, want 5s", cfg.VMCacheTTL)
	}

	t.Setenv("PDFM_MCP_VM_CACHE_TTL", "not-a-duration")
	if _, err := Load(nil, io.Discard); err == nil {
		t.Fatal("want error for bad TTL")
	}

	t.Setenv("PDFM_MCP_VM_CACHE_TTL", "0s")
	if _, err := Load(nil, io.Discard); err == nil {
		t.Fatal("want error for zero TTL")
	}
}

func TestLoadLogLevelStillWorks(t *testing.T) {
	clearEnv(t)
	cfg, err := Load([]string{"--log-level", "debug"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("LogLevel = %v", cfg.LogLevel)
	}
}

func TestLoadUnknownFlag(t *testing.T) {
	clearEnv(t)
	_, err := Load([]string{"--nope"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("err = %v, want unknown-flag error", err)
	}
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"PDFM_MCP_LOG_LEVEL", "PDFM_MCP_LOG_FILE", "PDFM_MCP_VM_ID", "PDFM_MCP_VM_CACHE_TTL",
	} {
		t.Setenv(k, "")
	}
}
