package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/orchestrator"
	"github.com/cjlapao/pdfm-mcp/internal/parallels"
)

// VMSequence returns the parallels_vm_sequence handler: it resolves the
// target VM, validates the whole sequence up front, then runs it in-process
// through the same handler code the standalone tools use (no MCP round
// trip).
func VMSequence(reg *orchestrator.Registry, runner *orchestrator.Runner) func(context.Context, *mcp.CallToolRequest, orchestrator.SequenceInput) (*mcp.CallToolResult, orchestrator.SequenceOutput, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, in orchestrator.SequenceInput) (*mcp.CallToolResult, orchestrator.SequenceOutput, error) {
		svc := parallels.GetInstance()
		target, err := resolvePowerTarget(svc, in.VMID)
		if err != nil {
			return nil, orchestrator.SequenceOutput{}, err
		}
		fleet := svc.VMID() == ""

		plan, verrs := orchestrator.Validate(in, reg, orchestrator.ValidateContext{Target: target, Fleet: fleet})
		if len(verrs) > 0 {
			msgs := make([]string, 0, len(verrs))
			for _, e := range verrs {
				msgs = append(msgs, e.Error())
			}
			return nil, orchestrator.SequenceOutput{}, fmt.Errorf("invalid sequence:\n%s", strings.Join(msgs, "\n"))
		}

		if in.ValidateOnly {
			out := orchestrator.SequenceOutput{VMID: target, Status: orchestrator.StatusValidated, Results: []orchestrator.StepResult{}}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: mustJSON(out)}}}, out, nil
		}

		// Progress: wire the client's progress token if present. A shallow
		// copy keeps the shared runner untouched (concurrent sequences).
		r := *runner
		if req != nil && req.Session != nil && req.Params != nil {
			if token := req.Params.GetProgressToken(); token != nil {
				r.Progress = func(done, total int, msg string) {
					_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
						ProgressToken: token,
						Progress:      float64(done),
						Total:         float64(total),
						Message:       msg,
					})
				}
			}
		}

		report, err := r.Run(ctx, plan)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil, orchestrator.SequenceOutput{}, fmt.Errorf("sequence cancelled: %w", err)
			}
			return nil, orchestrator.SequenceOutput{}, err
		}

		out := report.Output
		// Dual delivery: structured output plus the full JSON as the first
		// text content, then the returned images. Clients that drop
		// structuredContent still see the full report.
		content := make([]mcp.Content, 0, 1+len(report.ReturnedImages))
		content = append(content, &mcp.TextContent{Text: mustJSON(out)})
		for _, img := range report.ReturnedImages {
			content = append(content, &mcp.ImageContent{Data: img.Data, MIMEType: img.MIMEType})
		}
		return &mcp.CallToolResult{Content: content}, out, nil
	}
}

// RegisterParallelsVMSequence adds the parallels_vm_sequence tool for the
// given mode. The description is generated from the registry so it always
// lists the real step tools and argument names.
func RegisterParallelsVMSequence(s *mcp.Server, mode PowerMode, reg *orchestrator.Registry, runner *orchestrator.Runner) {
	input := powerInputSchema[orchestrator.SequenceInput](mode)
	// Normalize slice fields to plain "array" types (portability, same rule
	// as the output schema).
	normalizeNullArrays(input)
	register(s, &mcp.Tool{
		Name:        "parallels_vm_sequence",
		Description: sequenceDescription(reg, mode),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: truePtr(),
			IdempotentHint:  false,
			OpenWorldHint:   truePtr(),
		},
		InputSchema:  input,
		OutputSchema: outputSchemaFor[orchestrator.SequenceOutput]("parallels_vm_sequence"),
	}, VMSequence(reg, runner))
}

// sequenceDescription generates the tool description from the registry:
// the shared step/check grammar plus a "name(arg, arg?)" signature for every
// registered action (required args first, optional ones suffixed with ?).
func sequenceDescription(reg *orchestrator.Registry, mode PowerMode) string {
	var b strings.Builder
	b.WriteString("Runs a sequence of steps against one VM in a single call and returns every step's result; capture screenshots come back as images. ")
	b.WriteString("Step kinds: {\"tool\": \"<name>\", \"args\": {...}}, {\"wait\": {\"seconds\": N}}, {\"wait\": {\"until\": \"settled\"|\"changed\", from?, timeout_seconds?, poll_ms?, stable_polls?, threshold_percent?, tolerance?, max_dimension?}} (polls screenshots; settled = screen stopped changing, changed = differs from the capture step `from` or from the start; unmet before timeout = expectation_failed), {\"gate\": [checks]}. ")
	b.WriteString("Modifiers: id, when (skip if false), expect (assert own result), retry {max_attempts, delay_ms}, on_fail, on_error, quiet. ")
	b.WriteString("Checks (all must pass; negate inverts): exit_code{step, equals}, matches{step, field, regex}, contains{step, field, text}, image_changed{from, to, threshold_percent, tolerance}, succeeded{step}. ")
	b.WriteString("Step args are exactly the standalone tool's arguments without vm_id: ")
	for i, a := range reg.All() {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(a.Name)
		b.WriteString("(")
		b.WriteString(stepSignature(a))
		b.WriteString(")")
	}
	b.WriteString(". Max 50 steps, 120 s default total timeout, 8 returned images max.")
	return powerDescription(b.String(), mode)
}

// stepSignature renders "arg1, arg2?" for one action from its input schema.
// vm_id is omitted: the orchestrator owns the target (steps may not set it),
// so listing it would invite a validation error.
func stepSignature(a orchestrator.Action) string {
	if a.InputSchema == nil {
		return ""
	}
	required := make(map[string]bool, len(a.InputSchema.Required))
	for _, r := range a.InputSchema.Required {
		if r == "vm_id" {
			continue
		}
		required[r] = true
	}
	var reqNames, optNames []string
	for _, r := range a.InputSchema.Required {
		if r == "vm_id" {
			continue
		}
		reqNames = append(reqNames, r)
	}
	names := propertyNames(a.InputSchema)
	for _, n := range names {
		if n == "vm_id" {
			continue
		}
		if !required[n] {
			optNames = append(optNames, n+"?")
		}
	}
	return strings.Join(append(reqNames, optNames...), ", ")
}

// propertyNames returns the input schema's property names in a stable
// order: the inferred field order when present, else alphabetical.
func propertyNames(s *jsonschema.Schema) []string {
	if s.PropertyOrder != nil {
		return append([]string(nil), s.PropertyOrder...)
	}
	names := make([]string, 0, len(s.Properties))
	for n := range s.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		// SequenceOutput and StepResult are plain JSON types; a marshal
		// failure here is an internal bug, not an input problem.
		panic("tools: marshal sequence report: " + err.Error())
	}
	return string(raw)
}
