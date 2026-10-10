package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Outcome is what an action returns to the runner.
type Outcome struct {
	// Data is the tool's structured output as a generic map (used by checks
	// and the report).
	Data map[string]any
	// Images are the images carried by the tool's content blocks.
	Images []Image
}

// Action is one step type. Name is the MCP tool name.
type Action struct {
	Name        string
	Description string
	// VMScoped is true when the runner injects vm_id into the args in fleet
	// mode (and step-level vm_id is rejected at validation).
	VMScoped bool
	// ProducesImages marks actions whose results can carry images (used to
	// validate image_changed references up front).
	ProducesImages bool
	// InputSchema is the standalone tool's input schema without vm_id; it
	// drives the generated sequence description.
	InputSchema *jsonschema.Schema
	// Run executes one step. args is the final JSON object (vm_id already
	// injected in fleet mode). The handler must tolerate a nil request.
	Run func(ctx context.Context, args json.RawMessage) (Outcome, error)
	// ValidateArgs strictly validates the final args (unknown fields
	// included) so typos are caught before anything runs.
	ValidateArgs func(args json.RawMessage) error
}

// Registry maps tool names to actions, insertion-ordered.
type Registry struct {
	byName map[string]Action
	order  []string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byName: map[string]Action{}}
}

// Register adds an action. It panics on an empty or duplicate name: both
// are startup bugs.
func (r *Registry) Register(a Action) {
	if a.Name == "" {
		panic("orchestrator: action with empty name")
	}
	if _, ok := r.byName[a.Name]; ok {
		panic("orchestrator: duplicate action " + a.Name)
	}
	r.byName[a.Name] = a
	r.order = append(r.order, a.Name)
}

// Remove deletes the action registered under name and reports whether it
// existed. The relative order of the remaining actions is preserved.
func (r *Registry) Remove(name string) bool {
	if _, ok := r.byName[name]; !ok {
		return false
	}
	delete(r.byName, name)
	kept := make([]string, 0, len(r.order))
	for _, n := range r.order {
		if n != name {
			kept = append(kept, n)
		}
	}
	r.order = kept
	return true
}

// Filter keeps only the actions for which keep returns true and returns the
// names it removed, in registration order.
func (r *Registry) Filter(keep func(name string) bool) []string {
	var removed []string
	for _, n := range r.Names() {
		if !keep(n) {
			r.Remove(n)
			removed = append(removed, n)
		}
	}
	return removed
}

// Get returns the action registered under name.
func (r *Registry) Get(name string) (Action, bool) {
	a, ok := r.byName[name]
	return a, ok
}

// Names returns the registered action names in registration order.
func (r *Registry) Names() []string {
	return append([]string(nil), r.order...)
}

// All returns every action in registration order.
func (r *Registry) All() []Action {
	out := make([]Action, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.byName[n])
	}
	return out
}

// FromHandler adapts a standard tool handler into an Action. The handler
// is called in-process with a nil *mcp.CallToolRequest, so handlers must
// not dereference it.
func FromHandler[In, Out any](
	name, description string,
	vmScoped bool,
	inputSchema *jsonschema.Schema,
	h func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
) Action {
	strictDecode := func(args json.RawMessage) error {
		var in In
		if len(args) == 0 || string(args) == "null" {
			return nil
		}
		dec := json.NewDecoder(bytes.NewReader(args))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			return fmt.Errorf("invalid arguments for %s: %w", name, err)
		}
		return nil
	}
	return Action{
		Name:        name,
		Description: description,
		VMScoped:    vmScoped,
		InputSchema: inputSchema,
		ValidateArgs: func(args json.RawMessage) error {
			return strictDecode(args)
		},
		Run: func(ctx context.Context, args json.RawMessage) (Outcome, error) {
			var in In
			if len(args) > 0 && string(args) != "null" {
				dec := json.NewDecoder(bytes.NewReader(args))
				dec.DisallowUnknownFields()
				if err := dec.Decode(&in); err != nil {
					return Outcome{}, fmt.Errorf("invalid arguments for %s: %w", name, err)
				}
			}
			result, out, err := h(ctx, nil, in)
			if err != nil {
				return Outcome{}, err
			}
			data, err := outcomeData(out)
			if err != nil {
				return Outcome{}, fmt.Errorf("internal: encode %s output: %w", name, err)
			}
			var imgs []Image
			if result != nil {
				for _, c := range result.Content {
					if ic, ok := c.(*mcp.ImageContent); ok {
						imgs = append(imgs, Image{Data: ic.Data, MIMEType: ic.MIMEType})
					}
				}
			}
			return Outcome{Data: data, Images: imgs}, nil
		},
	}
}

// outcomeData converts the typed output to the generic map used by checks
// and the report.
func outcomeData(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
