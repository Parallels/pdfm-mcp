package tools

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
	"github.com/cjlapao/pdfm-mcp/internal/parallels/interfaces"
)

// VMInfoInput is the argument schema for the parallels_vm_info tool.
type VMInfoInput struct {
	VMID    string `json:"vm_id,omitempty" jsonschema:"VM id or name to target (required in fleet mode)"`
	Refresh bool   `json:"refresh,omitempty" jsonschema:"force a fresh prlctl list instead of using the in-memory cache (VM mode only; fleet mode always fetches live)"`
}

// VMInfoOutput is the structured result of the parallels_vm_info tool.
// VM is the exact prlctl JSON object for the target VM, so no field
// is ever dropped by the typed model. The field is `any` (not
// json.RawMessage) because the SDK's schema inference cannot express a
// free-form JSON object for a []byte-backed type.
type VMInfoOutput struct {
	VM        any    `json:"vm" jsonschema:"the exact prlctl JSON object of the target VM"`
	FetchedAt string `json:"fetched_at" jsonschema:"RFC3339 timestamp of when the data was fetched"`
	Source    string `json:"source" jsonschema:"cache when served from memory, refreshed when the cache was re-run (VM mode), live when prlctl was re-run for this call (fleet mode)"`
}

// VMInfo returns the full prlctl JSON configuration of one VM.
//
//	VM mode:  the --vm-id target, served from the in-memory cache
//	          (prlctl list <target> -a -i --json is the cache fetch).
//	Fleet mode: a specific VM by id or name, fetched live on every call
//	          with the same command as the list fetch, scoped to one VM:
//	          prlctl list <vm_id> -a -i --json.
func VMInfo(ctx context.Context, _ *mcp.CallToolRequest, in VMInfoInput) (*mcp.CallToolResult, VMInfoOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, VMInfoOutput{}, err
	}

	var (
		res    interfaces.VMRawResult
		source string
	)
	if svc.VMID() == "" {
		// Fleet mode: live single-VM fetch, never the cache.
		res, err = svc.VMInfoRef(ctx, target)
		if err != nil {
			return nil, VMInfoOutput{}, err
		}
		source = "live"
	} else {
		res, err = svc.VMRaw(ctx, in.Refresh)
		if err != nil {
			return nil, VMInfoOutput{}, err
		}
		source = "cache"
		if res.Refreshed {
			source = "refreshed"
		}
	}

	return nil, VMInfoOutput{
		VM:        res.Blob,
		FetchedAt: res.FetchedAt.UTC().Format(time.RFC3339),
		Source:    source,
	}, nil
}

// RegisterParallelsVMInfo adds the parallels_vm_info tool for the given
// mode.
func RegisterParallelsVMInfo(s *mcp.Server, mode PowerMode) {
	desc := "Returns the full prlctl JSON configuration of one VM. In VM mode it targets the --vm-id VM served from the in-memory cache; in fleet mode it fetches a specific VM live (prlctl list <vm_id> -a -i --json)."
	t := &mcp.Tool{
		Name:        "parallels_vm_info",
		Description: powerDescription(desc, mode),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		InputSchema: powerInputSchema[VMInfoInput](mode),
		// Same normalization as parallels_list_vms (no ["null","array"]
		// unions) for portability to single-type schema dialects.
		OutputSchema: outputSchemaFor[VMInfoOutput]("parallels_vm_info"),
	}
	mcp.AddTool(s, t, VMInfo)
}
