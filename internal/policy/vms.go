package policy

import "github.com/Parallels/pdfm-mcp/internal/parallels/models"

// VMAllowList restricts which VMs the tools may address. A nil or empty
// list means unrestricted.
type VMAllowList struct {
	entries []string
	set     map[string]struct{}
}

// NewVMAllowList builds a list from validated entries (see ParseVMList).
// It returns nil when entries is empty (no restriction).
func NewVMAllowList(entries []string) *VMAllowList {
	if len(entries) == 0 {
		return nil
	}
	a := &VMAllowList{
		entries: append([]string(nil), entries...),
		set:     make(map[string]struct{}, len(entries)),
	}
	for _, e := range entries {
		a.set[e] = struct{}{}
	}
	return a
}

// Active reports whether a restriction is configured.
func (a *VMAllowList) Active() bool { return a != nil && len(a.set) > 0 }

// Entries returns a copy of the configured entries.
func (a *VMAllowList) Entries() []string {
	if a == nil {
		return nil
	}
	return append([]string(nil), a.entries...)
}

// Contains reports an exact, case-sensitive match of ref against the
// entries. An inactive list contains everything.
func (a *VMAllowList) Contains(ref string) bool {
	if !a.Active() {
		return true
	}
	_, ok := a.set[ref]
	return ok
}

// AllowsRef decides whether the VM referenced by ref (an id or a name, as
// given by the caller) may be addressed. vms is the VM list used to resolve
// the other identifier form: the ref is allowed when it matches an entry
// exactly, or when it is the ID or Name of a listed VM whose ID or Name
// matches an entry. Anything else is denied (fail closed), including refs
// the list cannot resolve.
func (a *VMAllowList) AllowsRef(ref string, vms []models.VM) bool {
	if !a.Active() {
		return true
	}
	if a.Contains(ref) {
		return true
	}
	for i := range vms {
		if vms[i].ID == ref || vms[i].Name == ref {
			if a.AllowsVM(vms[i]) {
				return true
			}
		}
	}
	return false
}

// AllowsVM reports whether a listed VM is allowed: its ID or its Name
// matches an entry exactly.
func (a *VMAllowList) AllowsVM(vm models.VM) bool {
	if !a.Active() {
		return true
	}
	return a.Contains(vm.ID) || a.Contains(vm.Name)
}

// Filter returns the allowed subset of vms as a new slice (never nil); the
// input is not modified.
func (a *VMAllowList) Filter(vms []models.VM) []models.VM {
	out := make([]models.VM, 0, len(vms))
	for i := range vms {
		if a.AllowsVM(vms[i]) {
			out = append(out, vms[i])
		}
	}
	return out
}
