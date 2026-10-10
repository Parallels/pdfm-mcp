package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
)

// VMDecryptInput is the argument schema for the parallels_vm_decrypt tool.
// Password handling follows parallels_vm_encrypt (omittable only with
// dry_run).
type VMDecryptInput struct {
	VMID     string `json:"vm_id,omitempty" jsonschema:"VM id or name to target"`
	Password string `json:"password,omitempty" jsonschema:"current encryption password, 1-256 printable characters, fed to prlctl over stdin and never logged; required unless dry_run is true"`
	DryRun   bool   `json:"dry_run,omitempty" jsonschema:"check the prerequisites without decrypting (no password needed)"`
}

// VMDecrypt removes the encryption from an encrypted VM (prlctl decrypt).
// A wrong password is a clean tool error and leaves the VM encrypted.
func VMDecrypt(ctx context.Context, _ *mcp.CallToolRequest, in VMDecryptInput) (*mcp.CallToolResult, VMEncryptDecryptOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolveEncryptionTarget(svc, in.VMID, in.Password, in.DryRun)
	if err != nil {
		return nil, VMEncryptDecryptOutput{}, err
	}

	out, err := svc.Decrypt(ctx, target, in.Password, in.DryRun)
	if err != nil {
		return nil, VMEncryptDecryptOutput{}, err
	}
	return nil, VMEncryptDecryptOutput{DryRun: in.DryRun, Output: out}, nil
}

// RegisterParallelsVMDecrypt adds the parallels_vm_decrypt tool for the
// given mode.
func RegisterParallelsVMDecrypt(s *mcp.Server, mode PowerMode) {
	mcp.AddTool(s, &mcp.Tool{
		Name:         "parallels_vm_decrypt",
		Description:  powerDescription(descVMDecrypt, mode),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: truePtr(), IdempotentHint: false, OpenWorldHint: truePtr()},
		InputSchema:  powerInputSchema[VMDecryptInput](mode),
		OutputSchema: outputSchemaFor[VMEncryptDecryptOutput]("parallels_vm_decrypt"),
	}, VMDecrypt)
}
