package policy

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Parallels/pdfm-mcp/internal/parallels"
)

// Limits on the configured lists.
const (
	MaxToolEntries = 128
	MaxVMEntries   = 64
	maxToolPattern = 64
)

// splitList splits a comma-separated list, trims entries, drops empty ones
// and removes duplicates (first occurrence wins).
func splitList(raw string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, e := range strings.Split(raw, ",") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, dup := seen[e]; dup {
			continue
		}
		seen[e] = struct{}{}
		out = append(out, e)
	}
	return out
}

// validatePattern accepts a tool name ([a-z0-9_]+) optionally followed by a
// single trailing "*", or a bare "*".
func validatePattern(p string) error {
	if p == "" {
		return errors.New("empty entry")
	}
	if len(p) > maxToolPattern {
		return fmt.Errorf("entry longer than %d characters", maxToolPattern)
	}
	body := strings.TrimSuffix(p, "*")
	for _, r := range body {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		default:
			return fmt.Errorf("invalid entry %q: only lowercase letters, digits and _ are allowed, with an optional trailing *", p)
		}
	}
	return nil
}

// ParseToolList parses and validates a comma-separated tool list (exact
// names or trailing-* globs). An empty input yields nil.
func ParseToolList(raw string) ([]string, error) {
	out := splitList(raw)
	if len(out) > MaxToolEntries {
		return nil, fmt.Errorf("too many entries (%d, max %d)", len(out), MaxToolEntries)
	}
	for _, e := range out {
		if err := validatePattern(e); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ParseVMList parses and validates a comma-separated list of VM ids or
// names with parallels.ValidateVMRef. An empty input yields nil. Entries
// are case-sensitive; a VM name that contains a comma cannot be listed.
func ParseVMList(raw string) ([]string, error) {
	out := splitList(raw)
	if len(out) > MaxVMEntries {
		return nil, fmt.Errorf("too many entries (%d, max %d)", len(out), MaxVMEntries)
	}
	for i, e := range out {
		v, err := parallels.ValidateVMRef(e)
		if err != nil {
			return nil, fmt.Errorf("entry %d: %w", i+1, err)
		}
		out[i] = v
	}
	return out, nil
}
