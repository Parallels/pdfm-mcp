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
		"PDFM_MCP_READ_ONLY", "PDFM_MCP_ALLOW_TOOLS", "PDFM_MCP_DENY_TOOLS", "PDFM_MCP_ALLOW_VMS",
	} {
		t.Setenv(k, "")
	}
}

func TestUnresolvedPlaceholder(t *testing.T) {
	padded := "  ${user_config.vm_id} "
	cases := map[string]bool{
		"${user_config.vm_id}": true,
		padded:                 true,
		"${x}":                 true,
		"":                     false,
		"my-vm":                false,
		"vm$name":              false,
		"${open":               false,
		"close}":               false,
		"$HOME":                false,
	}
	for in, want := range cases {
		if got := unresolvedPlaceholder(in); got != want {
			t.Errorf("unresolvedPlaceholder(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPlaceholderGuardEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("PDFM_MCP_VM_ID", "${user_config.vm_id}")
	t.Setenv("PDFM_MCP_LOG_LEVEL", "${user_config.log_level}")
	t.Setenv("PDFM_MCP_VM_CACHE_TTL", "${user_config.ttl}")
	t.Setenv("PDFM_MCP_READ_ONLY", "${user_config.read_only}")
	t.Setenv("PDFM_MCP_ALLOW_TOOLS", "${user_config.allow_tools}")
	t.Setenv("PDFM_MCP_DENY_TOOLS", "${user_config.deny_tools}")
	t.Setenv("PDFM_MCP_ALLOW_VMS", "${user_config.allow_vms}")

	var buf strings.Builder
	cfg, err := Load(nil, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VMID != "" || cfg.LogLevel != slog.LevelInfo || cfg.VMCacheTTL != DefaultVMCacheTTL ||
		cfg.ReadOnly || cfg.AllowTools != nil || cfg.DenyTools != nil || cfg.AllowVMs != nil {
		t.Fatalf("placeholders must read as unset: %+v", cfg)
	}
	out := buf.String()
	for _, v := range []string{
		"PDFM_MCP_VM_ID", "PDFM_MCP_LOG_LEVEL", "PDFM_MCP_VM_CACHE_TTL", "PDFM_MCP_READ_ONLY",
		"PDFM_MCP_ALLOW_TOOLS", "PDFM_MCP_DENY_TOOLS", "PDFM_MCP_ALLOW_VMS",
	} {
		if n := strings.Count(out, "var="+v); n != 1 {
			t.Errorf("warn lines naming %s = %d, want 1\n%s", v, n, out)
		}
	}
	if strings.Contains(out, "user_config") {
		t.Fatalf("warning must never echo the value: %s", out)
	}
}

func TestPlaceholderLikeValuesStillValidated(t *testing.T) {
	clearEnv(t)
	t.Setenv("PDFM_MCP_VM_ID", "vm$name")
	cfg, err := Load(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VMID != "vm$name" {
		t.Fatalf("VMID = %q", cfg.VMID)
	}
}

func TestLoadPolicyFlagsAndEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("PDFM_MCP_READ_ONLY", "true")
	t.Setenv("PDFM_MCP_ALLOW_TOOLS", "parallels_list_vms")
	t.Setenv("PDFM_MCP_DENY_TOOLS", "parallels_vm_exec*")
	t.Setenv("PDFM_MCP_ALLOW_VMS", "env-vm")

	cfg, err := Load(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ReadOnly || len(cfg.AllowTools) != 1 || cfg.DenyTools[0] != "parallels_vm_exec*" || cfg.AllowVMs[0] != "env-vm" {
		t.Fatalf("env not applied: %+v", cfg)
	}

	// Flags beat env, including turning read-only off and clearing a list.
	cfg, err = Load([]string{"--read-only=false", "--allow-tools=", "--deny-tools", "parallels_vm_stop, parallels_vm_start", "--allow-vms", "a,b"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReadOnly || cfg.AllowTools != nil {
		t.Fatalf("flags must override env: %+v", cfg)
	}
	if len(cfg.DenyTools) != 2 || cfg.DenyTools[1] != "parallels_vm_start" {
		t.Fatalf("DenyTools = %v", cfg.DenyTools)
	}
	if len(cfg.AllowVMs) != 2 {
		t.Fatalf("AllowVMs = %v", cfg.AllowVMs)
	}

	cfg, err = Load([]string{"--read-only"}, io.Discard)
	if err != nil || !cfg.ReadOnly {
		t.Fatalf("--read-only: %+v %v", cfg, err)
	}
}

func TestLoadPolicyErrors(t *testing.T) {
	clearEnv(t)
	for _, args := range [][]string{
		{"--allow-tools", "Bad Name"},
		{"--deny-tools", "a*b"},
		{"--allow-vms", "bad\x01vm"},
		{"--vm-id", "vm1", "--allow-vms", "vm2"},
	} {
		if _, err := Load(args, io.Discard); err == nil {
			t.Errorf("Load(%q): want error", args)
		}
	}
	t.Setenv("PDFM_MCP_READ_ONLY", "maybe")
	if _, err := Load(nil, io.Discard); err == nil {
		t.Fatal("want error for bad boolean")
	}
}

func TestLoadVMModeAllowedTarget(t *testing.T) {
	clearEnv(t)
	cfg, err := Load([]string{"--vm-id", "vm1", "--allow-vms", "vm2,vm1"}, io.Discard)
	if err != nil || cfg.VMID != "vm1" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

func TestLoadDenyWinsDocumented(t *testing.T) {
	clearEnv(t)
	cfg, err := Load([]string{"--allow-tools", "parallels_vm_stop", "--deny-tools", "parallels_vm_stop"}, io.Discard)
	if err != nil {
		t.Fatalf("conflicting lists are not a config error: %v", err)
	}
	if len(cfg.AllowTools) != 1 || len(cfg.DenyTools) != 1 {
		t.Fatalf("both lists kept: %+v", cfg)
	}
}
