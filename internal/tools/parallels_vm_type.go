package tools

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
	"github.com/cjlapao/pdfm-mcp/internal/parallels/keys"
)

// maxTypeText bounds the text argument of parallels_vm_type.
const maxTypeText = 4096

// defaultTypeDelayMS is the default inter-key delay: 200 ms is slow enough
// that typical guests keep up with the keystrokes.
const defaultTypeDelayMS = 200

// TypeTextInput is the argument schema of parallels_vm_type.
type TypeTextInput struct {
	VMID string `json:"vm_id,omitempty" jsonschema:"VM id or name to type into"`
	Text string `json:"text" jsonschema:"ASCII text to type (letters, digits, space, punctuation). Escapes: \\n = Enter, \\t = Tab, \\\\ = literal backslash; a backslash before any other character is typed literally. Max 4096 characters"`
	// DelayMS is the inter-key delay in ms (0-5000); nil means the default
	// (200 ms). A pointer, not an int, so an explicit 0 stays 0.
	DelayMS *int `json:"delay_ms,omitempty" jsonschema:"delay in ms between key events (0-5000, default 200)"`
}

// TypeTextOutput is the structured result.
type TypeTextOutput struct {
	VMID   string `json:"vm_id" jsonschema:"the VM id or name the text was typed into"`
	Chars  int    `json:"chars" jsonschema:"number of characters typed"`
	Events int    `json:"events" jsonschema:"number of key events sent"`
	Output string `json:"output" jsonschema:"prlctl stdout (usually empty)"`
}

// VMTypeText types ASCII text into the VM. Each character is mapped to a
// key event (Shift handled automatically); the escapes \n and \t (and \\
// for a literal backslash) become Enter / Tab / backslash. Characters
// without a key mapping are rejected before anything is sent. The default
// inter-key delay is 200 ms (delay_ms 0-5000).
func VMTypeText(ctx context.Context, _ *mcp.CallToolRequest, in TypeTextInput) (*mcp.CallToolResult, TypeTextOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, TypeTextOutput{}, err
	}
	delay := defaultTypeDelayMS
	if in.DelayMS != nil {
		delay = *in.DelayMS
		if delay < 0 || delay > 5000 {
			return nil, TypeTextOutput{}, errors.New("delay_ms must be between 0 and 5000")
		}
	}
	if in.Text == "" {
		return nil, TypeTextOutput{}, errors.New("text must not be empty")
	}
	if len(in.Text) > maxTypeText {
		return nil, TypeTextOutput{}, fmt.Errorf("text is %d bytes, above the %d byte limit", len(in.Text), maxTypeText)
	}

	evs, err := keys.EventsForText(in.Text, delay)
	if err != nil {
		return nil, TypeTextOutput{}, err
	}
	out, err := svc.SendKey(ctx, target, evs)
	if err != nil {
		return nil, TypeTextOutput{}, err
	}
	return nil, TypeTextOutput{
		VMID:   target,
		Chars:  utf8.RuneCountInString(in.Text),
		Events: len(evs),
		Output: out,
	}, nil
}

// RegisterParallelsVMType adds the parallels_vm_type tool for the given
// mode.
func RegisterParallelsVMType(s *mcp.Server, mode PowerMode) {
	register(s, &mcp.Tool{
		Name: "parallels_vm_type",
		Description: powerDescription(
			descVMTypeText,
			mode),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: truePtr(),
			OpenWorldHint:   falsePtr(),
		},
		InputSchema:  powerInputSchema[TypeTextInput](mode),
		OutputSchema: outputSchemaFor[TypeTextOutput]("parallels_vm_type"),
	}, VMTypeText)
}
