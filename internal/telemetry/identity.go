// Package telemetry sends anonymised product-usage events to Amplitude
// (spec 024). It is a side channel: nothing here may block or fail a tool
// call, and no raw content (arguments, VM ids, error text, license serial)
// ever leaves the machine — only the allowlisted event properties from the
// spec are transmitted.
package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// defaultRunner runs one identity command, returning its stdout. Any
// failure (missing binary, non-zero exit, timeout) is an error and the
// caller degrades.
func defaultRunner(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

// Identity carries the Amplitude identifiers and the one license fact that
// is safe to transmit (the edition name — never the serial).
type Identity struct {
	DeviceID string // the macOS hardware UUID (IOPlatformUUID), or "unknown"
	UserID   string // sha256 hex of "pdfm-mcp:<hwUUID>:<serial|unlicensed>"
	Edition  string // Parallels Desktop license edition, or "unknown"
}

// CommandRunner runs a command and returns its stdout; tests inject fakes.
type CommandRunner func(ctx context.Context, name string, args ...string) (string, error)

// identityCommandTimeout bounds each identity lookup so a wedged ioreg or
// prlsrvctl can never stall startup.
const identityCommandTimeout = 10 * time.Second

// platformUUIDRE matches the IOPlatformUUID line of
// `ioreg -rd1 -c IOPlatformExpertDevice`.
var platformUUIDRE = regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([0-9A-Fa-f-]{36})"`)

// licenseInfo is the subset of `prlsrvctl info --license --json` we need.
type licenseInfo struct {
	Serial  string `json:"serial"`
	Edition string `json:"edition"`
}

// resolveIdentity fetches the hardware UUID and the Parallels Desktop
// license once (both through runner) and derives the anonymised pair.
// Every failure degrades: ioreg failure ⇒ "unknown" device, license failure
// ⇒ "unlicensed" user half — telemetry stays on with the best available id.
func resolveIdentity(ctx context.Context, runner CommandRunner) Identity {
	hwUUID := ""
	if out, err := runBounded(ctx, runner, "ioreg", "-rd1", "-c", "IOPlatformExpertDevice"); err == nil {
		if m := platformUUIDRE.FindStringSubmatch(out); m != nil {
			hwUUID = m[1]
		}
	}
	if hwUUID == "" {
		hwUUID = "unknown"
	}

	serial, edition := "", "unknown"
	if out, err := runBounded(ctx, runner, "prlsrvctl", "info", "--license", "--json"); err == nil {
		// prlsrvctl prefixes the JSON with an edition notice on some
		// builds (same quirk devops-service strips before parsing).
		var lic licenseInfo
		if i := strings.Index(out, "{"); i >= 0 && json.Unmarshal([]byte(out[i:]), &lic) == nil {
			serial = strings.TrimSpace(lic.Serial)
			if e := strings.TrimSpace(lic.Edition); e != "" {
				edition = e
			}
		}
	}

	return Identity{
		DeviceID: hwUUID,
		UserID:   userIDFor(hwUUID, serial),
		Edition:  edition,
	}
}

// userIDFor is the pure identity derivation (spec 024 section 2/5): the raw
// hardware UUID and license serial never leave the machine, only this hash.
func userIDFor(hwUUID, serial string) string {
	if serial == "" {
		serial = "unlicensed"
	}
	sum := sha256.Sum256([]byte("pdfm-mcp:" + hwUUID + ":" + serial))
	return hex.EncodeToString(sum[:])
}

// runBounded runs one identity command with a private deadline so a hung
// lookup cannot stall startup.
func runBounded(ctx context.Context, runner CommandRunner, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, identityCommandTimeout)
	defer cancel()
	return runner(cctx, name, args...)
}

// parseUUIDFromIOReg extracts the IOPlatformUUID from captured ioreg output;
// exported for tests (it validates the regexp against real output).
func parseUUIDFromIOReg(out string) string {
	if m := platformUUIDRE.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}
