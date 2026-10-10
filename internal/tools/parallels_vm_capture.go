package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/imaging"
	"github.com/Parallels/pdfm-mcp/internal/parallels"
)

// maxResultBytes bounds the final PNG handed back to the client. It guards
// the stdio line size and client-side image limits; smaller max_dimension
// values are the escape hatch.
const maxResultBytes = 10 << 20

// CaptureInput is the argument schema of parallels_vm_capture.
type CaptureInput struct {
	VMID           string `json:"vm_id,omitempty" jsonschema:"VM id or name to capture: the VM's UUID (the ID field) or its exact display name (the Name field) as returned by parallels_list_vms; pass the name exactly as shown (spaces are fine, no quotes needed)"`
	MaxDimension   int    `json:"max_dimension,omitempty" jsonschema:"longest edge in pixels the image is downscaled to (256-4096, default 1568)"`
	FullResolution bool   `json:"full_resolution,omitempty" jsonschema:"return the screenshot at native resolution without downscaling"`
}

// CaptureOutput is the structured metadata. The image itself is delivered
// as an image content block, never in structured output.
type CaptureOutput struct {
	VMID           string `json:"vm_id" jsonschema:"the VM id or name that was captured"`
	MIMEType       string `json:"mime_type" jsonschema:"image MIME type"`
	Width          int    `json:"width" jsonschema:"returned image width in pixels"`
	Height         int    `json:"height" jsonschema:"returned image height in pixels"`
	OriginalWidth  int    `json:"original_width" jsonschema:"native screen width in pixels"`
	OriginalHeight int    `json:"original_height" jsonschema:"native screen height in pixels"`
	Resized        bool   `json:"resized" jsonschema:"true when the image was downscaled"`
	Bytes          int    `json:"bytes" jsonschema:"size of the returned image in bytes"`
	CapturedAt     string `json:"captured_at" jsonschema:"RFC3339 UTC capture time"`
}

// VMCapture takes a screenshot of the VM's screen and returns it as a PNG
// image content block plus text metadata. The VM must be running; stopped,
// suspended and unknown VMs surface prlctl's error.
func VMCapture(ctx context.Context, _ *mcp.CallToolRequest, in CaptureInput) (*mcp.CallToolResult, CaptureOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, CaptureOutput{}, err
	}

	maxDim := imaging.DefaultMaxDimension
	if in.MaxDimension != 0 {
		if in.MaxDimension < imaging.MinMaxDimension || in.MaxDimension > imaging.MaxMaxDimension {
			return nil, CaptureOutput{}, fmt.Errorf("max_dimension %d out of range (%d-%d)", in.MaxDimension, imaging.MinMaxDimension, imaging.MaxMaxDimension)
		}
		maxDim = in.MaxDimension
	}

	res, err := svc.Capture(ctx, target)
	if err != nil {
		return nil, CaptureOutput{}, err
	}

	var (
		data    []byte
		width   int
		height  int
		origW   int
		origH   int
		resized bool
	)
	if in.FullResolution {
		if origW, origH, err = imaging.Dimensions(res.PNG); err != nil {
			return nil, CaptureOutput{}, err
		}
		data = res.PNG
		width, height = origW, origH
	} else {
		r, derr := imaging.Downscale(res.PNG, maxDim)
		if derr != nil {
			return nil, CaptureOutput{}, derr
		}
		data, width, height = r.Data, r.Width, r.Height
		origW, origH, resized = r.OriginalWidth, r.OriginalHeight, r.Resized
	}

	if len(data) > maxResultBytes {
		return nil, CaptureOutput{}, fmt.Errorf("capture result is %d bytes, above the %d byte limit; retry with a smaller max_dimension", len(data), maxResultBytes)
	}

	out := CaptureOutput{
		VMID:           target,
		MIMEType:       "image/png",
		Width:          width,
		Height:         height,
		OriginalWidth:  origW,
		OriginalHeight: origH,
		Resized:        resized,
		Bytes:          len(data),
		CapturedAt:     res.CapturedAt.UTC().Format(time.RFC3339),
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf(
				"Screenshot of VM %s: %dx%d px (native %dx%d), %d bytes, PNG.",
				target, width, height, origW, origH, len(data))},
			&mcp.ImageContent{Data: data, MIMEType: "image/png"}, // SDK base64-encodes on the wire
		},
	}, out, nil
}

// RegisterParallelsVMCapture adds the parallels_vm_capture tool for the
// given mode.
func RegisterParallelsVMCapture(s *mcp.Server, mode PowerMode) {
	register(s, &mcp.Tool{
		Name: "parallels_vm_capture",
		Description: powerDescription(
			descVMCapture,
			mode),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: falsePtr(),
		},
		InputSchema:  powerInputSchema[CaptureInput](mode),
		OutputSchema: outputSchemaFor[CaptureOutput]("parallels_vm_capture"),
	}, VMCapture)
}
