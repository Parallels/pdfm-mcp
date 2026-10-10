package policy

import (
	"slices"
	"strings"
	"testing"

	"github.com/cjlapao/pdfm-mcp/internal/parallels/models"
)

func TestClassificationTable(t *testing.T) {
	want := map[string]Class{
		"parallels_list_vms":           ClassRead,
		"parallels_vm_info":            ClassRead,
		"parallels_vm_capture":         ClassRead,
		"parallels_vm_observe":         ClassRead,
		"parallels_vm_exec_result":     ClassRead,
		"parallels_vm_exec":            ClassExec,
		"parallels_vm_exec_start":      ClassExec,
		"parallels_vm_exec_cancel":     ClassExec,
		"parallels_vm_send_key":        ClassInput,
		"parallels_vm_type":            ClassInput,
		"parallels_vm_start":           ClassPower,
		"parallels_vm_stop":            ClassPower,
		"parallels_vm_clone":           ClassPower,
		"parallels_vm_delete":          ClassDestructive,
		"parallels_vm_reclaim_disk":    ClassDestructive,
		"parallels_vm_snapshot_switch": ClassDestructive,
		"parallels_vm_encrypt":         ClassSecret,
		"parallels_vm_change_password": ClassSecret,
		"parallels_vm_sequence":        ClassSequence,
	}
	for name, c := range want {
		if got := Classify(name); got != c {
			t.Errorf("Classify(%s) = %s, want %s", name, got, c)
		}
		if !Known(name) {
			t.Errorf("Known(%s) = false", name)
		}
	}
}

func TestUnknownToolFailsClosed(t *testing.T) {
	if Known("parallels_vm_future") {
		t.Fatal("unexpectedly known")
	}
	if got := Classify("parallels_vm_future"); got != ClassDestructive {
		t.Fatalf("unknown class = %s, want destructive", got)
	}
	p, err := New(Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.Allows("parallels_vm_future") {
		t.Fatal("unknown tool must be hidden in read-only mode")
	}
}

func TestDecide(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		tool string
		want bool
	}{
		{"default allows all", Options{}, "parallels_vm_delete", true},
		{"read-only allows read", Options{ReadOnly: true}, "parallels_vm_info", true},
		{"read-only allows sequence", Options{ReadOnly: true}, SequenceTool, true},
		{"read-only hides power", Options{ReadOnly: true}, "parallels_vm_start", false},
		{"read-only hides exec_result? no, read", Options{ReadOnly: true}, "parallels_vm_exec_result", true},
		{"allow exact", Options{Allow: []string{"parallels_vm_start"}}, "parallels_vm_start", true},
		{"allow exact miss", Options{Allow: []string{"parallels_vm_start"}}, "parallels_vm_stop", false},
		{"allow glob", Options{Allow: []string{"parallels_vm_s*"}}, "parallels_vm_stop", true},
		{"glob is prefix only", Options{Allow: []string{"parallels_vm_s*"}}, "parallels_list_vms", false},
		{"deny exact", Options{Deny: []string{"parallels_vm_exec"}}, "parallels_vm_exec", false},
		{"deny exact does not cover exec_start", Options{Deny: []string{"parallels_vm_exec"}}, "parallels_vm_exec_start", true},
		{"deny glob covers exec_start", Options{Deny: []string{"parallels_vm_exec*"}}, "parallels_vm_exec_start", false},
		{"deny wins over allow", Options{Allow: []string{"parallels_vm_*"}, Deny: []string{"parallels_vm_stop"}}, "parallels_vm_stop", false},
		{"allow cannot widen read-only", Options{ReadOnly: true, Allow: []string{"parallels_vm_*"}}, "parallels_vm_start", false},
		{"bare star denies all", Options{Deny: []string{"*"}}, "parallels_vm_info", false},
	}
	for _, tc := range tests {
		p, err := New(tc.opts)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := p.Allows(tc.tool); got != tc.want {
			t.Errorf("%s: Allows(%s) = %v, want %v", tc.name, tc.tool, got, tc.want)
		}
	}
}

func TestDecideReasonNamesOnly(t *testing.T) {
	p, _ := New(Options{ReadOnly: true})
	d := p.Decide("parallels_vm_exec")
	if d.Allowed || !strings.Contains(d.Reason, "read-only") {
		t.Fatalf("decision = %+v", d)
	}
}

func TestNilPolicyAllows(t *testing.T) {
	var p *Policy
	if !p.Allows("anything") || p.Restricted() || p.ReadOnly() {
		t.Fatal("nil policy must be unrestricted")
	}
}

func TestNewRejectsBadPatterns(t *testing.T) {
	for _, bad := range []string{"", "Parallels", "a*b", "a**", "a b", "a-b", "*a"} {
		if _, err := New(Options{Allow: []string{bad}}); err == nil {
			t.Errorf("allow %q: want error", bad)
		}
		if _, err := New(Options{Deny: []string{bad}}); err == nil {
			t.Errorf("deny %q: want error", bad)
		}
	}
}

func TestUnmatchedPatterns(t *testing.T) {
	p, _ := New(Options{Allow: []string{"parallels_vm_*", "paralels_typo"}, Deny: []string{"nothing_*"}})
	got := p.UnmatchedPatterns()
	if !slices.Equal(got, []string{"paralels_typo", "nothing_*"}) {
		t.Fatalf("unmatched = %v", got)
	}
}

func TestParseToolList(t *testing.T) {
	got, err := ParseToolList(" a, b ,,a,c* ")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"a", "b", "c*"}) {
		t.Fatalf("got %v", got)
	}
	if got, err := ParseToolList("  "); err != nil || got != nil {
		t.Fatalf("empty: %v %v", got, err)
	}
	if _, err := ParseToolList("ok,Bad"); err == nil {
		t.Fatal("want error for uppercase")
	}
	if _, err := ParseToolList("a*b"); err == nil {
		t.Fatal("want error for inner glob")
	}
	many := make([]string, MaxToolEntries+1)
	for i := range many {
		many[i] = "t" + strings.Repeat("a", i%5) + string(rune('a'+i%26)) + strings.Repeat("b", i/26)
	}
	if _, err := ParseToolList(strings.Join(many, ",")); err == nil {
		t.Fatal("want error for too many entries")
	}
}

func TestParseVMList(t *testing.T) {
	got, err := ParseVMList(" win11 , 90f90e2b-e19f-4429-86b3-b5146bc860d4,win11")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"win11", "90f90e2b-e19f-4429-86b3-b5146bc860d4"}) {
		t.Fatalf("got %v", got)
	}
	if _, err := ParseVMList("ok,bad\x01name"); err == nil {
		t.Fatal("want error for control char")
	}
	var many []string
	for i := 0; i < MaxVMEntries+1; i++ {
		many = append(many, "vm"+strings.Repeat("x", i))
	}
	if _, err := ParseVMList(strings.Join(many, ",")); err == nil {
		t.Fatal("want error for >64 entries")
	}
	if got, err := ParseVMList(""); err != nil || got != nil {
		t.Fatalf("empty: %v %v", got, err)
	}
}

var sampleVMs = []models.VM{
	{ID: "id-1", Name: "alpha"},
	{ID: "id-2", Name: "beta"},
}

func TestVMAllowListRules(t *testing.T) {
	var none *VMAllowList
	if none.Active() || !none.AllowsRef("x", nil) || !none.Contains("x") {
		t.Fatal("nil list must be unrestricted")
	}
	if NewVMAllowList(nil) != nil {
		t.Fatal("empty entries must yield nil")
	}

	a := NewVMAllowList([]string{"alpha"})
	tests := []struct {
		ref  string
		want bool
	}{
		{"alpha", true},
		{"id-1", true},
		{"beta", false},
		{"id-2", false},
		{"Alpha", false},
		{"ghost", false},
		{"", false},
		{"alpha ", false},
	}
	for _, tc := range tests {
		if got := a.AllowsRef(tc.ref, sampleVMs); got != tc.want {
			t.Errorf("AllowsRef(%q) = %v, want %v", tc.ref, got, tc.want)
		}
	}
	// Without a VM list only exact entries match.
	if a.AllowsRef("id-1", nil) {
		t.Error("id-1 must not resolve without a list")
	}
	// Entry by id also allows the name.
	b := NewVMAllowList([]string{"id-2"})
	if !b.AllowsRef("beta", sampleVMs) || b.AllowsRef("alpha", sampleVMs) {
		t.Error("id entry resolution wrong")
	}
}

func TestVMAllowListFilter(t *testing.T) {
	a := NewVMAllowList([]string{"beta"})
	got := a.Filter(sampleVMs)
	if len(got) != 1 || got[0].ID != "id-2" {
		t.Fatalf("Filter = %+v", got)
	}
	if len(sampleVMs) != 2 {
		t.Fatal("input mutated")
	}
	if out := a.Filter(nil); out == nil || len(out) != 0 {
		t.Fatalf("Filter(nil) = %v, want empty non-nil", out)
	}
	if got := NewVMAllowList([]string{"none"}).Filter(sampleVMs); len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}
