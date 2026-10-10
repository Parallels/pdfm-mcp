package tools

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/Parallels/pdfm-mcp/internal/parallels/interfaces"
	"github.com/Parallels/pdfm-mcp/internal/policy"
)

// vmAllow is the process-wide VM allow-list (spec 019), set by server.New.
// nil or inactive means unrestricted. It is consulted by resolvePowerTarget,
// so every VM-scoped tool — standalone or as a sequence step — is covered.
var vmAllow atomic.Pointer[policy.VMAllowList]

// SetVMAllowList installs the VM allow-list; nil clears it.
func SetVMAllowList(a *policy.VMAllowList) { vmAllow.Store(a) }

// vmAllowList returns the active allow-list, or nil when unrestricted.
func vmAllowList() *policy.VMAllowList {
	a := vmAllow.Load()
	if !a.Active() {
		return nil
	}
	return a
}

// authorizeVM enforces the allow-list on one VM reference. An exact match
// against an entry is accepted without further lookups; otherwise the VM
// list cache resolves the ref to the other identifier form (id <-> name).
// A cache failure denies (fail closed). The error names the rule but never
// lists other VMs.
func authorizeVM(svc interfaces.ParallelsService, ref string) error {
	a := vmAllowList()
	if a == nil || a.Contains(ref) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if res, err := svc.ListVMs(ctx, false); err == nil && a.AllowsRef(ref, res.VMs) {
		return nil
	}
	return errors.New("this VM is not permitted: the server restricts VM access with --allow-vms (PDFM_MCP_ALLOW_VMS)")
}
