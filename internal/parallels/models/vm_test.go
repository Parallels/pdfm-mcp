package models

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestNormalizeFillsNilSlices(t *testing.T) {
	var v VM
	v.Normalize()
	if v.Network.IPAddresses == nil {
		t.Fatal("IPAddresses still nil after Normalize")
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(`"ipAddresses":null`)) {
		t.Fatalf("null leaked into encoding: %s", b)
	}
}

func TestNormalizeKeepsExistingValues(t *testing.T) {
	v := VM{Network: Network{IPAddresses: []NetworkIPAddress{{Type: "shared", IP: "10.0.0.5"}}}}
	v.Normalize()
	if len(v.Network.IPAddresses) != 1 || v.Network.IPAddresses[0].IP != "10.0.0.5" {
		t.Fatalf("Normalize clobbered values: %+v", v.Network.IPAddresses)
	}
}
