package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Parallels/pdfm-mcp/internal/parallels/interfaces"
)

const (
	// sendKeyTimeout bounds one send-key-event invocation, including any
	// user-requested delays between events.
	sendKeyTimeout = 300 * time.Second
	// maxKeyCode / maxScancode bound the event identifiers.
	maxKeyCode  = 255
	maxScancode = 255
	// maxEventDelayMS bounds one event's delay.
	maxEventDelayMS = 60000
	// maxEvents bounds one call. The worst case for a 4096-char type_text
	// with alternating Shift is ~3 events per char (12288).
	maxEvents = 20000
	// maxTotalDelayMS bounds the sum of all event delays.
	maxTotalDelayMS = 300000
)

// validateEvents bounds the event sequence before anything is executed.
func validateEvents(events []interfaces.KeyEvent) error {
	if len(events) == 0 {
		return errors.New("at least one key event is required")
	}
	if len(events) > maxEvents {
		return fmt.Errorf("%d events exceeds the %d event limit", len(events), maxEvents)
	}
	var totalDelay int
	for i, e := range events {
		hasKey := e.Key != nil
		hasScan := e.Scancode != nil
		if hasKey && hasScan {
			return fmt.Errorf("event %d: key and scancode are mutually exclusive", i)
		}
		if !hasKey && !hasScan && e.Delay == nil {
			return fmt.Errorf("event %d: needs a key, a scancode or a delay", i)
		}
		if hasKey && (*e.Key < 1 || *e.Key > maxKeyCode) {
			return fmt.Errorf("event %d: key %d out of range 1-%d", i, *e.Key, maxKeyCode)
		}
		if hasScan && (*e.Scancode < 0 || *e.Scancode > maxScancode) {
			return fmt.Errorf("event %d: scancode %d out of range 0-%d", i, *e.Scancode, maxScancode)
		}
		switch e.Event {
		case "", "press", "release":
		default:
			return fmt.Errorf("event %d: unknown event %q (want press, release, or omitted)", i, e.Event)
		}
		if e.Delay != nil {
			if *e.Delay < 0 || *e.Delay > maxEventDelayMS {
				return fmt.Errorf("event %d: delay %d out of range 0-%d ms", i, *e.Delay, maxEventDelayMS)
			}
			totalDelay += *e.Delay
		}
	}
	if totalDelay > maxTotalDelayMS {
		return fmt.Errorf("total delay %d ms exceeds the %d ms limit", totalDelay, maxTotalDelayMS)
	}
	return nil
}

// SendKey sends a sequence of keyboard events to the VM via
// `prlctl send-key-event <vm> -j`, feeding the serialized events on
// stdin. Any prlctl failure (launch, non-zero exit, timeout) is an error;
// there is no guest exit-code concept for key events.
func (s *service) SendKey(ctx context.Context, vmRef string, events []interfaces.KeyEvent) (string, error) {
	if !s.enabled {
		return "", ErrNotEnabled
	}
	ref, err := ValidateVMRef(vmRef)
	if err != nil {
		return "", err
	}
	if err := validateEvents(events); err != nil {
		return "", err
	}
	data, err := json.Marshal(events)
	if err != nil {
		return "", fmt.Errorf("encode key events: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, sendKeyTimeout)
	defer cancel()

	start := time.Now()
	stdout, stderr, exitCode, err := s.runner.RunStdin(runCtx, s.prlctlPath, string(data), "send-key-event", ref, "-j")
	if err != nil {
		// The error string carries the VM ref and prlctl's stderr; the
		// event content itself is never logged.
		s.logger.Warn("prlctl send-key-event failed", "vm", ref, "events", len(events))
		return "", err
	}
	if exitCode != 0 {
		s.logger.Warn("prlctl send-key-event failed", "vm", ref, "events", len(events), "exit_code", exitCode)
		return "", fmt.Errorf("prlctl send-key-event %s failed (exit %d): %s", ref, exitCode, strings.TrimSpace(stderr))
	}
	s.logger.Info("prlctl send-key-event completed", "vm", ref, "events", len(events), "took", time.Since(start))
	return stdout, nil
}
