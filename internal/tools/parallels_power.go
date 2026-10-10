package tools

import (
	"context"
	"errors"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/parallels"
	"github.com/Parallels/pdfm-mcp/internal/parallels/interfaces"
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
	VMID string `json:"vm_id,omitempty" jsonschema:"VM id or name to target: the VM's UUID (the ID field) or its exact display name (the Name field) as returned by parallels_list_vms; pass the name exactly as shown (spaces are fine, no quotes needed)"`
}

// PowerStopInput is the argument schema of parallels_vm_stop.
type PowerStopInput struct {
	VMID      string `json:"vm_id,omitempty" jsonschema:"VM id or name to target: the VM's UUID (the ID field) or its exact display name (the Name field) as returned by parallels_list_vms; pass the name exactly as shown (spaces are fine, no quotes needed)"`
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
// explicit value (a single-VM-scoped server must not touch other VMs). The
// VM allow-list (--allow-vms) is enforced here for every VM-scoped tool.
//
// The reference is unquoted first (parallels.UnquoteVMRef, spec 025) so a
// name the model shell-quoted still authorizes against the allow-list and
// still reaches prlctl as the plain name.
func resolvePowerTarget(svc interfaces.ParallelsService, explicit string) (string, error) {
	explicit = parallels.UnquoteVMRef(explicit)
	if explicit != "" {
		if svc.VMID() != "" {
			return "", errors.New("vm_id must be omitted: this server targets a specific VM (started with --vm-id)")
		}
		if err := authorizeVM(svc, explicit); err != nil {
			return "", err
		}
		return explicit, nil
	}
	if svc.VMID() == "" {
		return "", errors.New("vm_id is required: this server runs in fleet mode (started without --vm-id)")
	}
	if err := authorizeVM(svc, svc.VMID()); err != nil {
		return "", err
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
// in fleet mode, makes vm_id required (on top of whatever the type already
// declares required, e.g. the exec tool's command). Nullable pointer fields
// are split into single-type anyOf branches (see splitNullableUnions) so
// the wire schema stays portable to single-type dialects.
func powerInputSchema[T any](mode PowerMode) *jsonschema.Schema {
	schema, err := jsonschema.For[T](&jsonschema.ForOptions{})
	if err != nil {
		panic("tools: power input schema: " + err.Error())
	}
	splitNullableUnions(schema)
	pinBareNodes(schema)
	if mode == PowerFleet {
		for _, r := range schema.Required {
			if r == "vm_id" {
				return schema
			}
		}
		schema.Required = append(schema.Required, "vm_id")
	}
	return schema
}

// powerDescription builds the mode-aware tool description.
func powerDescription(what string, mode PowerMode) string {
	if mode == PowerFleet {
		return what + " The vm_id argument is required: the VM's UUID (the ID field) or its exact display name (the Name field) as returned by parallels_list_vms; names with spaces are fine as-is, no quotes needed."
	}
	return what + " Targets the VM configured with --vm-id; the vm_id argument must be omitted."
}

func truePtr() *bool {
	b := true
	return &b
}

func falsePtr() *bool {
	b := false
	return &b
}
