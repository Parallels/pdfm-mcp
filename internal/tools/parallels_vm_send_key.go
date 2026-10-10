package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
	"github.com/cjlapao/pdfm-mcp/internal/parallels/interfaces"
	"github.com/cjlapao/pdfm-mcp/internal/parallels/keys"
)

// KeyEventArg is one element of an events sequence — the documented
// prlctl -j format, including the short alias keys k/s/d. Plain values
// keep the input schema free of null unions; 0 means "not provided".
type KeyEventArg struct {
	Key      string `json:"key,omitempty" jsonschema:"key name (enter, f5, a, left_shift, PRL_KEY_ENTER, …) or numeric keycode"`
	K        string `json:"k,omitempty" jsonschema:"alias for key (prlctl JSON compatibility)"`
	Scancode int    `json:"scancode,omitempty" jsonschema:"keyboard scan code (0-255); alternative to key"`
	S        int    `json:"s,omitempty" jsonschema:"alias for scancode (prlctl JSON compatibility)"`
	Event    string `json:"event,omitempty" jsonschema:"press or release; omit for a tap (press+release)"`
	Delay    int    `json:"delay,omitempty" jsonschema:"delay in milliseconds before this event (0-60000)"`
	D        int    `json:"d,omitempty" jsonschema:"alias for delay (prlctl JSON compatibility)"`
}

// SendKeyInput is the argument schema of parallels_vm_send_key.
type SendKeyInput struct {
	VMID     string        `json:"vm_id,omitempty" jsonschema:"VM id or name to send the key event(s) to"`
	Key      string        `json:"key,omitempty" jsonschema:"single key: name (enter, f5, a, left_shift, …) or numeric code; omit when using events"`
	Scancode int           `json:"scancode,omitempty" jsonschema:"single key by scan code (0-255); alternative to key"`
	Event    string        `json:"event,omitempty" jsonschema:"press or release; omit for a tap (press+release)"`
	Delay    int           `json:"delay,omitempty" jsonschema:"delay in milliseconds before the key (0-60000)"`
	Events   []KeyEventArg `json:"events,omitempty" jsonschema:"full event sequence; each entry: {key|scancode, event, delay} or {delay} for a pause"`
}

// SendKeyOutput is the structured result.
type SendKeyOutput struct {
	VMID   string `json:"vm_id" jsonschema:"the VM id or name the events were sent to"`
	Events int    `json:"events" jsonschema:"number of key events sent"`
	Output string `json:"output" jsonschema:"prlctl stdout (usually empty)"`
}

// keyEventFromArg converts one documented-format event (aliases allowed)
// to the wire form.
func keyEventFromArg(a KeyEventArg) (interfaces.KeyEvent, error) {
	if a.Key != "" && a.K != "" && a.Key != a.K {
		return interfaces.KeyEvent{}, fmt.Errorf("key and k disagree (%q vs %q)", a.Key, a.K)
	}
	name := a.Key
	if name == "" {
		name = a.K
	}
	if a.Scancode != 0 && a.S != 0 && a.Scancode != a.S {
		return interfaces.KeyEvent{}, fmt.Errorf("scancode and s disagree (%d vs %d)", a.Scancode, a.S)
	}
	sc := a.Scancode
	if sc == 0 {
		sc = a.S
	}
	if a.Delay != 0 && a.D != 0 && a.Delay != a.D {
		return interfaces.KeyEvent{}, fmt.Errorf("delay and d disagree (%d vs %d)", a.Delay, a.D)
	}
	delay := a.Delay
	if delay == 0 {
		delay = a.D
	}
	if name == "" && sc == 0 && delay == 0 {
		return interfaces.KeyEvent{}, errors.New("needs a key, a scancode or a delay")
	}
	if name != "" && sc != 0 {
		return interfaces.KeyEvent{}, errors.New("provide either key or scancode, not both")
	}
	ev := interfaces.KeyEvent{Event: a.Event}
	if name != "" {
		code, err := keys.CodeFor(name)
		if err != nil {
			return interfaces.KeyEvent{}, err
		}
		c := code
		ev.Key = &c
	} else if sc != 0 {
		// sc == 0 with a delay is a pure pause: no identifier is sent.
		s := sc
		ev.Scancode = &s
	}
	if delay > 0 {
		d := delay
		ev.Delay = &d
	}
	return ev, nil
}

// VMSendKey sends keyboard events to the VM: either one single event or
// a full sequence (modifier combos, pauses). The events are serialized to
// prlctl's documented JSON format and fed to `send-key-event <vm> -j` on
// stdin.
func VMSendKey(ctx context.Context, _ *mcp.CallToolRequest, in SendKeyInput) (*mcp.CallToolResult, SendKeyOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, SendKeyOutput{}, err
	}

	var events []interfaces.KeyEvent
	if len(in.Events) > 0 {
		if in.Key != "" || in.Scancode != 0 {
			return nil, SendKeyOutput{}, errors.New("provide either events or a single key/scancode, not both")
		}
		for i, a := range in.Events {
			ev, err := keyEventFromArg(a)
			if err != nil {
				return nil, SendKeyOutput{}, fmt.Errorf("events[%d]: %w", i, err)
			}
			events = append(events, ev)
		}
	} else {
		if in.Key == "" && in.Scancode == 0 {
			return nil, SendKeyOutput{}, errors.New("provide a key, a scancode, or an events sequence")
		}
		if in.Key != "" && in.Scancode != 0 {
			return nil, SendKeyOutput{}, errors.New("provide either key or scancode, not both")
		}
		ev := interfaces.KeyEvent{Event: in.Event}
		if in.Key != "" {
			code, err := keys.CodeFor(in.Key)
			if err != nil {
				return nil, SendKeyOutput{}, err
			}
			c := code
			ev.Key = &c
		} else {
			s := in.Scancode
			ev.Scancode = &s
		}
		if in.Delay > 0 {
			d := in.Delay
			ev.Delay = &d
		}
		events = []interfaces.KeyEvent{ev}
	}

	out, err := svc.SendKey(ctx, target, events)
	if err != nil {
		return nil, SendKeyOutput{}, err
	}
	return nil, SendKeyOutput{VMID: target, Events: len(events), Output: out}, nil
}

// RegisterParallelsVMSendKey adds the parallels_vm_send_key tool for the
// given mode.
func RegisterParallelsVMSendKey(s *mcp.Server, mode PowerMode) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "parallels_vm_send_key",
		Description: powerDescription(
			descVMSendKey,
			mode),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: truePtr(),
			OpenWorldHint:   falsePtr(),
		},
		InputSchema:  powerInputSchema[SendKeyInput](mode),
		OutputSchema: outputSchemaFor[SendKeyOutput]("parallels_vm_send_key"),
	}, VMSendKey)
}
