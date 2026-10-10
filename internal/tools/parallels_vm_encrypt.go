package tools

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
	"github.com/cjlapao/pdfm-mcp/internal/parallels/interfaces"
)

// VMEncryptInput is the argument schema for the parallels_vm_encrypt tool.
// Password uses omitempty so a dry_run call may omit it; a real run without
// a password is rejected by the handler (same convention as vm_id in VM
// mode: the schema stays permissive, the handler enforces the contract).
type VMEncryptInput struct {
	VMID     string `json:"vm_id,omitempty" jsonschema:"VM id or name to target"`
	Password string `json:"password,omitempty" jsonschema:"encryption password, 1-256 printable characters, fed to prlctl over stdin and never logged; required unless dry_run is true"`
	DryRun   bool   `json:"dry_run,omitempty" jsonschema:"check the prerequisites without encrypting (no password needed)"`
}

// VMEncryptDecryptOutput is the shared result of parallels_vm_encrypt and
// parallels_vm_decrypt.
type VMEncryptDecryptOutput struct {
	DryRun bool   `json:"dry_run" jsonschema:"true when only the prerequisites were checked"`
	Output string `json:"output" jsonschema:"prlctl's trimmed stdout (the success or prerequisite message)"`
}

// resolveEncryptionTarget applies the mode contract and the password
// presence rule shared by parallels_vm_encrypt and parallels_vm_decrypt.
func resolveEncryptionTarget(svc interfaces.ParallelsService, vmID, password string, dryRun bool) (string, error) {
	target, err := resolvePowerTarget(svc, vmID)
	if err != nil {
		return "", err
	}
	if !dryRun && password == "" {
		return "", errors.New("password is required for this operation (only a dry_run call may omit it)")
	}
	return target, nil
}

// VMEncrypt encrypts a VM and all of its data with a password
// (prlctl encrypt), or checks the prerequisites with dry_run.
func VMEncrypt(ctx context.Context, _ *mcp.CallToolRequest, in VMEncryptInput) (*mcp.CallToolResult, VMEncryptDecryptOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolveEncryptionTarget(svc, in.VMID, in.Password, in.DryRun)
	if err != nil {
		return nil, VMEncryptDecryptOutput{}, err
	}

	out, err := svc.Encrypt(ctx, target, in.Password, in.DryRun)
	if err != nil {
		return nil, VMEncryptDecryptOutput{}, err
	}
	return nil, VMEncryptDecryptOutput{DryRun: in.DryRun, Output: out}, nil
}

// RegisterParallelsVMEncrypt adds the parallels_vm_encrypt tool for the
// given mode.
func RegisterParallelsVMEncrypt(s *mcp.Server, mode PowerMode) {
	mcp.AddTool(s, &mcp.Tool{
		Name:         "parallels_vm_encrypt",
		Description:  powerDescription(descVMEncrypt, mode),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: truePtr(), IdempotentHint: false, OpenWorldHint: truePtr()},
		InputSchema:  powerInputSchema[VMEncryptInput](mode),
		OutputSchema: outputSchemaFor[VMEncryptDecryptOutput]("parallels_vm_encrypt"),
	}, VMEncrypt)
}
