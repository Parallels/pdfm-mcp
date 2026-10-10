package tools

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/parallels"
	"github.com/Parallels/pdfm-mcp/internal/parallels/models"
)

const (
	// defaultSearchLimit bounds results when the caller does not set one.
	defaultSearchLimit = 10
	// maxSearchLimit caps the limit argument.
	maxSearchLimit = 50
	// maxQueryLength / maxStateLength bound untrusted input (spec 026).
	maxQueryLength = 200
	maxStateLength = 50
)

// SearchVmsInput is the argument schema for the parallels_search_vms tool.
type SearchVmsInput struct {
	Query   string `json:"query" jsonschema:"case-insensitive substring to match against the VM name or UUID (required)"`
	State   string `json:"state,omitempty" jsonschema:"optional exact state filter, case-insensitive (e.g. running, stopped, paused, suspended); empty = any state"`
	Limit   int    `json:"limit,omitempty" jsonschema:"maximum number of results (default 10, max 50)"`
	Refresh bool   `json:"refresh,omitempty" jsonschema:"force a fresh prlctl list instead of using the in-memory cache"`
}

// SearchVMSummary is one compact result entry: just enough for the caller
// to target the VM with any other tool (id or name, spec 025 contract).
type SearchVMSummary struct {
	ID    string `json:"id" jsonschema:"the VM UUID - use as vm_id"`
	Name  string `json:"name" jsonschema:"the VM display name - use as vm_id (may contain spaces; pass exactly as shown, no quotes needed)"`
	State string `json:"state" jsonschema:"the VM state (e.g. running, stopped, paused, suspended)"`
	OS    string `json:"os,omitempty" jsonschema:"the guest OS family when known"`
	IP    string `json:"ip,omitempty" jsonschema:"the first non-empty IP address when the VM has one"`
}

// SearchVmsOutput is the structured result of the parallels_search_vms
// tool. Deliberately compact: the full prlctl detail stays in
// parallels_list_vms / parallels_vm_info (spec 026).
type SearchVmsOutput struct {
	Vms       []SearchVMSummary `json:"vms" jsonschema:"the matching VMs, best match first"`
	Count     int               `json:"count" jsonschema:"number of results returned (at most limit)"`
	Total     int               `json:"total" jsonschema:"number of VMs scanned (the size of the list the search ran over)"`
	FetchedAt string            `json:"fetched_at" jsonschema:"RFC3339 timestamp of the last cache refresh"`
	Source    string            `json:"source" jsonschema:"cache when served from memory, refreshed when prlctl was re-run for this call"`
}

// vmMatchRank is the ranking score of one VM against the query:
// 0 exact name, 1 exact UUID, 2 name prefix, 3 name substring, 4 UUID
// substring; -1 means no match (spec 026).
func vmMatchRank(name, id, query string) int {
	lname, lid, lq := strings.ToLower(name), strings.ToLower(id), strings.ToLower(query)
	switch {
	case lname == lq:
		return 0
	case lid == lq:
		return 1
	case strings.HasPrefix(lname, lq):
		return 2
	case strings.Contains(lname, lq):
		return 3
	case strings.Contains(lid, lq):
		return 4
	default:
		return -1
	}
}

// firstIPAddress returns the first non-empty IP address of a VM, or "".
func firstIPAddress(vm models.VM) string {
	for _, a := range vm.Network.IPAddresses {
		if a.IP != "" {
			return a.IP
		}
	}
	return ""
}

// SearchVms finds VMs by a case-insensitive substring of their name or
// UUID, optionally filtered by exact state, and returns a compact summary
// (spec 026). It runs over the same in-memory list cache as
// parallels_list_vms: no extra prlctl call on a cache hit.
func SearchVms(ctx context.Context, _ *mcp.CallToolRequest, in SearchVmsInput) (*mcp.CallToolResult, SearchVmsOutput, error) {
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return nil, SearchVmsOutput{}, errors.New("query is required: a case-insensitive substring of the VM name or UUID")
	}
	if len(query) > maxQueryLength {
		return nil, SearchVmsOutput{}, errors.New("invalid query: longer than 200 characters")
	}
	state := strings.ToLower(strings.TrimSpace(in.State))
	if len(state) > maxStateLength {
		return nil, SearchVmsOutput{}, errors.New("invalid state: longer than 50 characters")
	}
	lim := in.Limit
	switch {
	case lim <= 0:
		lim = defaultSearchLimit
	case lim > maxSearchLimit:
		lim = maxSearchLimit
	}

	svc := parallels.GetInstance()
	res, err := svc.ListVMs(ctx, in.Refresh)
	if err != nil {
		return nil, SearchVmsOutput{}, err
	}

	vms := res.VMs
	if a := vmAllowList(); a != nil {
		// --allow-vms: disallowed VMs never leave the server (same rule
		// as parallels_list_vms).
		vms = a.Filter(vms)
	}

	type ranked struct {
		vm   models.VM
		rank int
	}
	var matches []ranked
	for _, vm := range vms {
		if state != "" && strings.ToLower(vm.State) != state {
			continue
		}
		r := vmMatchRank(vm.Name, vm.ID, query)
		if r < 0 {
			continue
		}
		matches = append(matches, ranked{vm: vm, rank: r})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].rank != matches[j].rank {
			return matches[i].rank < matches[j].rank
		}
		return strings.ToLower(matches[i].vm.Name) < strings.ToLower(matches[j].vm.Name)
	})
	if len(matches) > lim {
		matches = matches[:lim]
	}

	out := make([]SearchVMSummary, 0, len(matches))
	for _, m := range matches {
		out = append(out, SearchVMSummary{
			ID:    m.vm.ID,
			Name:  m.vm.Name,
			State: m.vm.State,
			OS:    m.vm.OS,
			IP:    firstIPAddress(m.vm),
		})
	}
	source := "cache"
	if res.Refreshed {
		source = "refreshed"
	}
	return nil, SearchVmsOutput{
		Vms:       out,
		Count:     len(out),
		Total:     len(vms),
		FetchedAt: res.FetchedAt.UTC().Format(time.RFC3339),
		Source:    source,
	}, nil
}

// RegisterParallelsSearchVms adds the parallels_search_vms tool to the
// server. The caller must only register it when the Parallels module is
// enabled. The input schema is inferred from SearchVmsInput by the SDK
// (same pattern as parallels_list_vms; the portable-schema sweep in the
// protocol tests covers it).
func RegisterParallelsSearchVms(s *mcp.Server) {
	t := &mcp.Tool{
		Name: "parallels_search_vms",
		Description: "Finds VMs by a case-insensitive substring of the VM name or UUID and returns a compact summary (id, name, state, os, ip) instead of the full configuration. " +
			"Use this to locate a VM: parallels_list_vms returns the full prlctl detail of every VM, which is large and may be truncated by the client. " +
			"Then use a result's id or name as the vm_id argument of any VM-scoped tool.",
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: true},
		OutputSchema: outputSchemaFor[SearchVmsOutput]("parallels_search_vms"),
	}
	register(s, t, SearchVms)
}
