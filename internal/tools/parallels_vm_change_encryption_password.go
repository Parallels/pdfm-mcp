package tools

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/parallels"
)

// VMChangeEncryptionPasswordInput is the argument schema for the
// parallels_vm_change_encryption_password tool. Both passwords use omitempty so the
// schema stays permissive; the handler requires both (the service
// validates length and character set).
type VMChangeEncryptionPasswordInput struct {
	VMID            string `json:"vm_id,omitempty" jsonschema:"VM id or name to target: the VM's UUID (the ID field) or its exact display name (the Name field) as returned by parallels_list_vms; pass the name exactly as shown (spaces are fine, no quotes needed)"`
	CurrentPassword string `json:"current_password,omitempty" jsonschema:"current encryption password, 1-256 printable characters, fed to prlctl over stdin and never logged"`
	NewPassword     string `json:"new_password,omitempty" jsonschema:"new encryption password, 1-256 printable characters, fed to prlctl over stdin and never logged"`
}

// VMChangeEncryptionPasswordOutput is the structured result of the
// parallels_vm_change_encryption_password tool.
type VMChangeEncryptionPasswordOutput struct {
	Output string `json:"output" jsonschema:"prlctl's trimmed stdout (the success message)"`
}

// VMChangeEncryptionPassword changes the DISK ENCRYPTION password of an
// encrypted VM (prlctl change-passwd) — not a guest OS/account password:
// prlctl reads the current password, then the new one, over stdin, with no
// confirmation prompt.
func VMChangeEncryptionPassword(ctx context.Context, _ *mcp.CallToolRequest, in VMChangeEncryptionPasswordInput) (*mcp.CallToolResult, VMChangeEncryptionPasswordOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, VMChangeEncryptionPasswordOutput{}, err
	}
	if in.CurrentPassword == "" {
		return nil, VMChangeEncryptionPasswordOutput{}, errors.New("current_password is required")
	}
	if in.NewPassword == "" {
		return nil, VMChangeEncryptionPasswordOutput{}, errors.New("new_password is required")
	}

	out, err := svc.ChangePasswd(ctx, target, in.CurrentPassword, in.NewPassword)
	if err != nil {
		return nil, VMChangeEncryptionPasswordOutput{}, err
	}
	return nil, VMChangeEncryptionPasswordOutput{Output: out}, nil
}

// RegisterParallelsVMChangeEncryptionPassword adds the parallels_vm_change_encryption_password
// tool for the given mode.
func RegisterParallelsVMChangeEncryptionPassword(s *mcp.Server, mode PowerMode) {
	register(s, &mcp.Tool{
		Name:         "parallels_vm_change_encryption_password",
		Description:  powerDescription(descVMChangeEncryptionPassword, mode),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: truePtr(), IdempotentHint: false, OpenWorldHint: truePtr()},
		InputSchema:  powerInputSchema[VMChangeEncryptionPasswordInput](mode),
		OutputSchema: outputSchemaFor[VMChangeEncryptionPasswordOutput]("parallels_vm_change_encryption_password"),
	}, VMChangeEncryptionPassword)
}
