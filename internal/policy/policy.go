// Package policy implements server-side governance: which MCP tools are
// exposed (read-only mode, allow/deny lists, a tool classification table)
// and which VMs the tools may address (VM allow-list). See spec 019.
package policy

import (
	"fmt"
	"sort"
	"strings"
)

// Class is the risk class of a tool.
type Class string

// The tool classes. Read-only mode exposes ClassRead (and the sequence
// meta-tool, whose steps are filtered by the same policy) and nothing else.
const (
	// ClassRead tools observe and never change VM or host state.
	ClassRead Class = "read"
	// ClassInput tools inject keyboard input into a VM.
	ClassInput Class = "input"
	// ClassPower tools change VM state in a recoverable way (power
	// operations, clone, snapshot create).
	ClassPower Class = "power"
	// ClassExec tools run arbitrary commands inside a guest.
	ClassExec Class = "exec"
	// ClassDestructive tools can lose data or are irreversible. Unknown
	// tools are classified here (fail closed).
	ClassDestructive Class = "destructive"
	// ClassSecret tools handle passwords (encryption lifecycle).
	ClassSecret Class = "secret"
	// ClassSequence is the orchestrator meta-tool. It has no risk of its
	// own: it can only run the steps that survive the policy filter.
	ClassSequence Class = "sequence"
)

// SequenceTool is the name of the orchestrator meta-tool.
const SequenceTool = "parallels_vm_sequence"

// classes is the classification table. Every tool the server registers
// must appear here (enforced by a test in internal/server); names of tools
// that are not built yet are pre-classified. Unknown names are destructive.
//
// Classification rules of thumb: a tool is "read" only when no argument
// combination can change state; a tool whose arguments select between a
// harmless and a harmful mode (reclaim_disk --estimate) takes the class of
// its harmful mode.
var classes = map[string]Class{
	// Fleet / observation.
	"parallels_list_vms":           ClassRead,
	"parallels_search_vms":         ClassRead,
	"parallels_vm_info":            ClassRead,
	"parallels_vm_capture":         ClassRead,
	"parallels_vm_observe":         ClassRead,
	"parallels_vm_exec_result":     ClassRead,
	"parallels_vm_snapshot_list":   ClassRead,
	"parallels_vm_sequence":        ClassSequence,
	"parallels_vm_start":           ClassPower,
	"parallels_vm_resume":          ClassPower,
	"parallels_vm_pause":           ClassPower,
	"parallels_vm_suspend":         ClassPower,
	"parallels_vm_restart":         ClassPower,
	"parallels_vm_reset":           ClassPower,
	"parallels_vm_reset_uptime":    ClassPower,
	"parallels_vm_stop":            ClassPower,
	"parallels_vm_clone":           ClassPower,
	"parallels_vm_snapshot_create": ClassPower,
	"parallels_vm_exec":            ClassExec,
	"parallels_vm_exec_start":      ClassExec,
	"parallels_vm_exec_cancel":     ClassExec,
	"parallels_vm_send_key":        ClassInput,
	"parallels_vm_type":            ClassInput,
	"parallels_vm_delete":          ClassDestructive,
	"parallels_vm_reclaim_disk":    ClassDestructive,
	"parallels_vm_snapshot_delete": ClassDestructive,
	"parallels_vm_snapshot_switch": ClassDestructive,
	"parallels_vm_encrypt":         ClassSecret,
	"parallels_vm_decrypt":         ClassSecret,
	"parallels_vm_change_password": ClassSecret,
}

// Classify returns the class of a tool. Unknown names are ClassDestructive
// so that a new, unclassified tool is hidden in read-only mode.
func Classify(name string) Class {
	if c, ok := classes[name]; ok {
		return c
	}
	return ClassDestructive
}

// Known reports whether name is in the classification table.
func Known(name string) bool {
	_, ok := classes[name]
	return ok
}

// KnownNames returns every classified tool name, sorted.
func KnownNames() []string {
	out := make([]string, 0, len(classes))
	for n := range classes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Options configures a Policy. Allow and Deny hold validated entries (see
// ParseToolList): exact tool names or names with a trailing "*".
type Options struct {
	ReadOnly bool
	Allow    []string
	Deny     []string
}

// Decision is the outcome of a policy check. Reason is set when the tool is
// hidden and never contains anything but names and flag names.
type Decision struct {
	Allowed bool
	Reason  string
}

// Policy decides which tools are exposed.
type Policy struct {
	readOnly bool
	allow    []string
	deny     []string
}

// New validates the options and returns a Policy.
func New(o Options) (*Policy, error) {
	for _, e := range o.Allow {
		if err := validatePattern(e); err != nil {
			return nil, fmt.Errorf("allow-tools: %w", err)
		}
	}
	for _, e := range o.Deny {
		if err := validatePattern(e); err != nil {
			return nil, fmt.Errorf("deny-tools: %w", err)
		}
	}
	return &Policy{
		readOnly: o.ReadOnly,
		allow:    append([]string(nil), o.Allow...),
		deny:     append([]string(nil), o.Deny...),
	}, nil
}

// Restricted reports whether any tool-level restriction is configured.
func (p *Policy) Restricted() bool {
	return p != nil && (p.readOnly || len(p.allow) > 0 || len(p.deny) > 0)
}

// ReadOnly reports whether read-only mode is on.
func (p *Policy) ReadOnly() bool { return p != nil && p.readOnly }

// Decide evaluates one tool name. Order: deny list (always wins), allow
// list (when set, only matching names), read-only (only ClassRead and the
// sequence meta-tool). All restrictions intersect: read-only cannot be
// widened by --allow-tools.
func (p *Policy) Decide(name string) Decision {
	if p == nil {
		return Decision{Allowed: true}
	}
	if matchAny(p.deny, name) {
		return Decision{Reason: "matched --deny-tools"}
	}
	if len(p.allow) > 0 && !matchAny(p.allow, name) {
		return Decision{Reason: "not in --allow-tools"}
	}
	if p.readOnly {
		if c := Classify(name); c != ClassRead && c != ClassSequence {
			return Decision{Reason: "read-only mode (class " + string(c) + ")"}
		}
	}
	return Decision{Allowed: true}
}

// Allows reports whether the tool is exposed.
func (p *Policy) Allows(name string) bool { return p.Decide(name).Allowed }

// UnmatchedPatterns returns the allow/deny entries that match no classified
// tool name (probable typos; names only, for a startup warning).
func (p *Policy) UnmatchedPatterns() []string {
	if p == nil {
		return nil
	}
	known := KnownNames()
	var out []string
	for _, pat := range append(append([]string(nil), p.allow...), p.deny...) {
		found := false
		for _, n := range known {
			if matchPattern(pat, n) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, pat)
		}
	}
	return out
}

func matchAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if matchPattern(p, name) {
			return true
		}
	}
	return false
}

// matchPattern matches an exact name or a trailing-"*" prefix glob.
func matchPattern(pattern, name string) bool {
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(name, prefix)
	}
	return pattern == name
}
