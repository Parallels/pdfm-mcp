package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Captured from a real Apple silicon Mac (shape only; the UUID below is a
// fixed fixture, not a real device).
const sampleIOReg = `+-o Root  <class IORegistryEntry, id 0x100000100, retain 0x5>
  +-o IOPlatformExpertDevice  <class IOPlatformExpertDevice, id 0x100000322, retain 0x10>
      {
        "IOPlatformSerialNumber" = "NOTREAL0000"
      "IOPlatformUUID" = "9AE1D424-FBA5-51B4-89DE-E8AF1998A164"
        "IOTemplateIndex" = 0
      }
`

func TestParseUUIDFromIOReg(t *testing.T) {
	got := parseUUIDFromIOReg(sampleIOReg)
	if got != "9AE1D424-FBA5-51B4-89DE-E8AF1998A164" {
		t.Fatalf("parseUUIDFromIOReg = %q", got)
	}
	if got := parseUUIDFromIOReg("no uuid here"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestUserIDForDeterministicAndDistinct(t *testing.T) {
	a1 := userIDFor("HW-1", "SERIAL-1")
	a2 := userIDFor("HW-1", "SERIAL-1")
	if a1 != a2 {
		t.Fatalf("not deterministic: %q vs %q", a1, a2)
	}
	if a1 == userIDFor("HW-2", "SERIAL-1") {
		t.Fatal("different hardware must give different user ids")
	}
	if a1 == userIDFor("HW-1", "SERIAL-2") {
		t.Fatal("different license must give different user ids")
	}
	// 64 hex chars of sha256.
	if len(a1) != 64 || a1 != strings.ToLower(a1) {
		t.Fatalf("bad hash shape: %q", a1)
	}
}

func TestUserIDForUnlicensedFallback(t *testing.T) {
	if userIDFor("HW-1", "") != userIDFor("HW-1", "unlicensed") {
		t.Fatal("empty serial must derive the same id as the explicit unlicensed marker")
	}
	if userIDFor("HW-1", "") == userIDFor("HW-1", "REAL-SERIAL") {
		t.Fatal("licensed and unlicensed ids must differ")
	}
	// The raw serial must not appear in the hash output (trivially true for
	// sha256, asserted for documentation of the contract).
	if strings.Contains(userIDFor("HW-1", "TOPSECRET-SERIAL"), "TOPSECRET") {
		t.Fatal("raw serial leaked into user id")
	}
}

// fakeRunner returns canned stdout per command name.
func fakeRunner(t *testing.T, outs map[string]string, errs map[string]error) CommandRunner {
	t.Helper()
	return func(_ context.Context, name string, _ ...string) (string, error) {
		if err, ok := errs[name]; ok {
			return "", err
		}
		return outs[name], nil
	}
}

func TestResolveIdentityTable(t *testing.T) {
	const (
		uuid   = "9AE1D424-FBA5-51B4-89DE-E8AF1998A164"
		serial = "AAAA-BBBB-CCCC"
	)
	licenseJSON := func() string {
		b, _ := json.Marshal(licenseInfo{Serial: serial, Edition: "Pro"})
		return string(b)
	}

	tests := []struct {
		name       string
		outs       map[string]string
		errs       map[string]error
		deviceID   string
		wantUser   string
		wantEition string
	}{
		{
			name:       "both resolved",
			outs:       map[string]string{"ioreg": sampleIOReg, "prlsrvctl": licenseJSON()},
			deviceID:   uuid,
			wantUser:   userIDFor(uuid, serial),
			wantEition: "Pro",
		},
		{
			name:       "license failure degrades to unlicensed",
			outs:       map[string]string{"ioreg": sampleIOReg},
			errs:       map[string]error{"prlsrvctl": errors.New("not running")},
			deviceID:   uuid,
			wantUser:   userIDFor(uuid, "unlicensed"),
			wantEition: "unknown",
		},
		{
			name:       "license without serial degrades to unlicensed",
			outs:       map[string]string{"ioreg": sampleIOReg, "prlsrvctl": `{"status":"TRIAL"}`},
			deviceID:   uuid,
			wantUser:   userIDFor(uuid, "unlicensed"),
			wantEition: "unknown",
		},
		{
			name:       "ioreg failure degrades to unknown device",
			outs:       map[string]string{"prlsrvctl": licenseJSON()},
			errs:       map[string]error{"ioreg": errors.New("no ioreg")},
			deviceID:   "unknown",
			wantUser:   userIDFor("unknown", serial),
			wantEition: "Pro",
		},
		{
			name:       "prlsrvctl noise prefix before the JSON",
			outs:       map[string]string{"ioreg": sampleIOReg, "prlsrvctl": "This feature is not available in this edition of Parallels Desktop. \n" + licenseJSON()},
			deviceID:   uuid,
			wantUser:   userIDFor(uuid, serial),
			wantEition: "Pro",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id := resolveIdentity(context.Background(), fakeRunner(t, tc.outs, tc.errs))
			if id.DeviceID != tc.deviceID {
				t.Fatalf("DeviceID = %q, want %q", id.DeviceID, tc.deviceID)
			}
			if id.UserID != tc.wantUser {
				t.Fatalf("UserID = %q, want %q", id.UserID, tc.wantUser)
			}
			if id.Edition != tc.wantEition {
				t.Fatalf("Edition = %q, want %q", id.Edition, tc.wantEition)
			}
		})
	}
}
