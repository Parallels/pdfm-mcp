package tools

import (
	"context"
	"errors"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
	"github.com/cjlapao/pdfm-mcp/internal/parallels/interfaces"
)

// PowerMode is how a power tool resolves its target VM.
type PowerMode int

const (
	// PowerFleet is the mode where the server has no --vm-id; the vm_id
	// argument is required.
	PowerFleet PowerMode = iota
	// PowerVM is the mode where the server targets the --vm-id VM; an
	// explicit vm_id argument is rejected.
	PowerVM
)

// PowerVMInput is the argument schema of the simple power tools.
type PowerVMInput struct {
	VMID string `json:"vm_id,omitempty" jsonschema:"VM id or name to target"`
}

// PowerStopInput is the argument schema of parallels_vm_stop.
type PowerStopInput struct {
	VMID      string `json:"vm_id,omitempty" jsonschema:"VM id or name to target"`
	Kill      bool   `json:"kill,omitempty" jsonschema:"force a hard shutdown (prlctl stop --kill)"`
	DropState bool   `json:"drop_state,omitempty" jsonschema:"reset a suspended VM to a full shutdown so it boots afresh next time (prlctl stop --drop-state)"`
}

// PowerOutput is the structured result of a power operation.
type PowerOutput struct {
	VMID   string `json:"vm_id" jsonschema:"the VM id or name the command targeted"`
	Output string `json:"output" jsonschema:"prlctl output (usually empty on success)"`
}

// resolvePowerTarget applies the mode contract to the raw vm_id argument:
// fleet mode requires it, VM mode uses the configured target and rejects an
// explicit value (a single-VM-scoped server must not touch other VMs).
func resolvePowerTarget(svc interfaces.ParallelsService, explicit string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		if svc.VMID() != "" {
			return "", errors.New("vm_id must be omitted: this server targets a specific VM (started with --vm-id)")
		}
		return explicit, nil
	}
	if svc.VMID() == "" {
		return "", errors.New("vm_id is required: this server runs in fleet mode (started without --vm-id)")
	}
	return svc.VMID(), nil
}

// runPower is the shared handler body of the power tools.
func runPower(ctx context.Context, op interfaces.PowerOp, inVMID string, kill, dropState bool) (*mcp.CallToolResult, PowerOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, inVMID)
	if err != nil {
		return nil, PowerOutput{}, err
	}
	out, err := svc.Power(ctx, op, target, kill, dropState)
	if err != nil {
		return nil, PowerOutput{}, err
	}
	return nil, PowerOutput{VMID: target, Output: out}, nil
}

// powerInputSchema infers the input schema for the given input type and,
// in fleet mode, makes vm_id required.
func powerInputSchema[T any](mode PowerMode) *jsonschema.Schema {
	schema, err := jsonschema.For[T](&jsonschema.ForOptions{})
	if err != nil {
		panic("tools: power input schema: " + err.Error())
	}
	if mode == PowerFleet {
		schema.Required = []string{"vm_id"}
	}
	return schema
}

// powerDescription builds the mode-aware tool description.
func powerDescription(what string, mode PowerMode) string {
	if mode == PowerFleet {
		return what + " The vm_id argument (a VM id or name) is required."
	}
	return what + " Targets the VM configured with --vm-id; the vm_id argument must be omitted."
}

func truePtr() *bool {
	b := true
	return &b
}
