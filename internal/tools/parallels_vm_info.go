package tools

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
)

// VMInfoInput is the argument schema for the parallels_vm_info tool.
type VMInfoInput struct {
	Refresh bool `json:"refresh,omitempty" jsonschema:"force a fresh prlctl list instead of using the in-memory cache"`
}

// VMInfoOutput is the structured result of the parallels_vm_info tool.
// VM is the exact cached prlctl JSON object for the target VM, so no field
// is ever dropped by the typed model. The field is `any` (not
// json.RawMessage) because the SDK's schema inference cannot express a
// free-form JSON object for a []byte-backed type.
type VMInfoOutput struct {
	VM        any    `json:"vm" jsonschema:"the exact prlctl JSON object of the target VM"`
	FetchedAt string `json:"fetched_at" jsonschema:"RFC3339 timestamp of the last cache refresh"`
	Source    string `json:"source" jsonschema:"cache when served from memory, refreshed when prlctl was re-run for this call"`
}

// VMInfo returns the full cached configuration of the target VM. It is only
// registered in VM mode (server started with --vm-id).
func VMInfo(ctx context.Context, _ *mcp.CallToolRequest, in VMInfoInput) (*mcp.CallToolResult, VMInfoOutput, error) {
	res, err := parallels.GetInstance().VMRaw(ctx, in.Refresh)
	if err != nil {
		return nil, VMInfoOutput{}, err
	}

	source := "cache"
	if res.Refreshed {
		source = "refreshed"
	}
	return nil, VMInfoOutput{
		VM:        res.Blob,
		FetchedAt: res.FetchedAt.UTC().Format(time.RFC3339),
		Source:    source,
	}, nil
}

// RegisterParallelsVMInfo adds the parallels_vm_info tool to the server.
// The caller must only register it when the Parallels module is enabled and
// a target VM is configured.
func RegisterParallelsVMInfo(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "parallels_vm_info",
		Description: "Returns the full cached prlctl JSON configuration of the target VM (the server must have been started with --vm-id). Served from the in-memory cache.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		// Same normalization as parallels_list_vms (no ["null","array"]
		// unions) for portability to single-type schema dialects.
		OutputSchema: outputSchemaFor[VMInfoOutput]("parallels_vm_info"),
	}, VMInfo)
}
