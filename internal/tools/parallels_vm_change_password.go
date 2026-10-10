package tools

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
)

// VMChangePasswordInput is the argument schema for the
// parallels_vm_change_password tool. Both passwords use omitempty so the
// schema stays permissive; the handler requires both (the service
// validates length and character set).
type VMChangePasswordInput struct {
	VMID            string `json:"vm_id,omitempty" jsonschema:"VM id or name to target"`
	CurrentPassword string `json:"current_password,omitempty" jsonschema:"current encryption password, 1-256 printable characters, fed to prlctl over stdin and never logged"`
	NewPassword     string `json:"new_password,omitempty" jsonschema:"new encryption password, 1-256 printable characters, fed to prlctl over stdin and never logged"`
}

// VMChangePasswordOutput is the structured result of the
// parallels_vm_change_password tool.
type VMChangePasswordOutput struct {
	Output string `json:"output" jsonschema:"prlctl's trimmed stdout (the success message)"`
}

// VMChangePassword changes the encryption password of an encrypted VM
// (prlctl change-passwd): prlctl reads the current password, then the new
// one, over stdin — no confirmation prompt.
func VMChangePassword(ctx context.Context, _ *mcp.CallToolRequest, in VMChangePasswordInput) (*mcp.CallToolResult, VMChangePasswordOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, VMChangePasswordOutput{}, err
	}
	if in.CurrentPassword == "" {
		return nil, VMChangePasswordOutput{}, errors.New("current_password is required")
	}
	if in.NewPassword == "" {
		return nil, VMChangePasswordOutput{}, errors.New("new_password is required")
	}

	out, err := svc.ChangePasswd(ctx, target, in.CurrentPassword, in.NewPassword)
	if err != nil {
		return nil, VMChangePasswordOutput{}, err
	}
	return nil, VMChangePasswordOutput{Output: out}, nil
}

// RegisterParallelsVMChangePassword adds the parallels_vm_change_password
// tool for the given mode.
func RegisterParallelsVMChangePassword(s *mcp.Server, mode PowerMode) {
	register(s, &mcp.Tool{
		Name:         "parallels_vm_change_password",
		Description:  powerDescription(descVMChangePassword, mode),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: truePtr(), IdempotentHint: false, OpenWorldHint: truePtr()},
		InputSchema:  powerInputSchema[VMChangePasswordInput](mode),
		OutputSchema: outputSchemaFor[VMChangePasswordOutput]("parallels_vm_change_password"),
	}, VMChangePassword)
}
