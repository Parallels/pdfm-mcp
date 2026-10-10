package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Parallels/pdfm-mcp/internal/parallels/models"
)

func seedCache(t *testing.T) *vmCache {
	t.Helper()
	blob := json.RawMessage(`{"ID":"abc","Name":"test"}`)
	var vm models.VM
	if err := json.Unmarshal(blob, &vm); err != nil {
		t.Fatal(err)
	}
	c := newVMCache(time.Hour)
	c.store([]*models.VM{&vm}, map[string]json.RawMessage{"abc": blob})
	return c
}

func noopFetch(context.Context) error { return nil }

func TestGetRawReturnsCopy(t *testing.T) {
	c := seedCache(t)

	blob, _, _, err := c.getRaw(context.Background(), "abc", noopFetch, false)
	if err != nil {
		t.Fatal(err)
	}
	blob[0] = 'X' // mutate the returned copy
	blob2, _, _, err := c.getRaw(context.Background(), "abc", noopFetch, false)
	if err != nil {
		t.Fatal(err)
	}
	if blob2[0] == 'X' {
		t.Fatal("getRaw must return a copy, not the stored blob")
	}
}

func TestGetRawMissingID(t *testing.T) {
	c := seedCache(t)
	_, _, _, err := c.getRaw(context.Background(), "ghost", noopFetch, false)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestInvalidateID(t *testing.T) {
	c := seedCache(t)
	c.invalidateID("abc")
	if !c.stale() {
		t.Fatal("cache should be stale after invalidateID")
	}
	c.mu.RLock()
	_, ok := c.raw["abc"]
	c.mu.RUnlock()
	if ok {
		t.Fatal("blob should be dropped")
	}
}
