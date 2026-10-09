package tools

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
	"github.com/cjlapao/pdfm-mcp/internal/parallels/models"
)

// ListVmsInput is the argument schema for the parallels_list_vms tool.
type ListVmsInput struct {
	Refresh bool `json:"refresh,omitempty" jsonschema:"force a fresh prlctl list instead of using the in-memory cache"`
}

// ListVmsOutput is the structured result of the parallels_list_vms tool.
type ListVmsOutput struct {
	Vms       []models.VM `json:"vms" jsonschema:"the virtual machines with full prlctl detail; only the target VM in VM mode"`
	Count     int         `json:"count" jsonschema:"number of VMs in the list"`
	FetchedAt string      `json:"fetched_at" jsonschema:"RFC3339 timestamp of the last cache refresh"`
	Source    string      `json:"source" jsonschema:"cache when served from memory, refreshed when prlctl was re-run for this call"`
}

// ListVms serves the cached Parallels VM list (fleet mode: all VMs, VM mode:
// only the target VM).
func ListVms(ctx context.Context, _ *mcp.CallToolRequest, in ListVmsInput) (*mcp.CallToolResult, ListVmsOutput, error) {
	res, err := parallels.GetInstance().ListVMs(ctx, in.Refresh)
	if err != nil {
		return nil, ListVmsOutput{}, err
	}

	vms := res.VMs
	if vms == nil {
		vms = make([]models.VM, 0)
	}
	source := "cache"
	if res.Refreshed {
		source = "refreshed"
	}
	return nil, ListVmsOutput{
		Vms:       vms,
		Count:     len(vms),
		FetchedAt: res.FetchedAt.UTC().Format(time.RFC3339),
		Source:    source,
	}, nil
}

// RegisterParallelsListVms adds the parallels_list_vms tool to the server.
// The caller must only register it when the Parallels module is enabled.
//
// The output schema is generated with the SDK's own inference library and
// normalized (no ["null","array"] unions) for portability to single-type
// schema dialects — see outputschema.go.
func RegisterParallelsListVms(s *mcp.Server) {
	t := &mcp.Tool{
		Name:         "parallels_list_vms",
		Description:  "Lists Parallels Desktop virtual machines with full configuration (prlctl list -a -i), served from the in-memory cache. When the server runs in VM mode (started with --vm-id) only the target VM is returned.",
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: true},
		OutputSchema: outputSchemaFor[ListVmsOutput]("parallels_list_vms"),
	}
	mcp.AddTool(s, t, ListVms)
}
