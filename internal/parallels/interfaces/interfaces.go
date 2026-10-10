// Package interfaces defines the contracts of the Parallels Desktop module.
package interfaces

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Parallels/pdfm-mcp/internal/parallels/models"
)

// PowerOp identifies one of the prlctl power subcommands. The values are
// the exact argv element passed to prlctl; they come from a fixed set, so
// no external input can become a command or flag.
type PowerOp string

// The supported power operations.
const (
	PowerStart       PowerOp = "start"        // start a stopped VM (or resume a paused one)
	PowerResume      PowerOp = "resume"       // resume a paused VM
	PowerPause       PowerOp = "pause"        // pause a running VM
	PowerSuspend     PowerOp = "suspend"      // suspend a running VM (state kept)
	PowerRestart     PowerOp = "restart"      // graceful shutdown + start
	PowerReset       PowerOp = "reset"        // hard shutdown + start
	PowerResetUptime PowerOp = "reset-uptime" // reset the uptime counter
	PowerStop        PowerOp = "stop"         // graceful shutdown (or hard with kill)
)

// VMListResult is the outcome of a ListVMs call.
type VMListResult struct {
	VMs       []models.VM
	FetchedAt time.Time
	// Refreshed is true when this call executed a prlctl list command
	// (cold/stale cache or forced); false means it was served from memory.
	Refreshed bool
}

// VMRawResult is the outcome of a VMRaw call.
type VMRawResult struct {
	Blob      json.RawMessage // the exact prlctl JSON object for the target VM
	FetchedAt time.Time
	Refreshed bool
}

// CaptureResult is the outcome of a Capture call.
type CaptureResult struct {
	PNG        []byte    // the exact PNG written by prlctl
	CapturedAt time.Time // when the capture finished (UTC)
}

// KeyEvent is one validated keyboard event in the wire form (spec 006).
// Key (1-255) and Scancode (0-255) are mutually exclusive; both nil
// means a pure delay pause. Event is "" (tap = press+release),
// "press" or "release". Delay is milliseconds before the event. The
// JSON field names match prlctl's documented -j format exactly.
type KeyEvent struct {
	Key      *int   `json:"key,omitempty"`
	Scancode *int   `json:"scancode,omitempty"`
	Event    string `json:"event,omitempty"`
	Delay    *int   `json:"delay,omitempty"`
}

// ExecResult is the outcome of an Exec call.
type ExecResult struct {
	Stdout string
	Stderr string
	// ExitCode is the guest command's exit code; -1 when no process ran.
	ExitCode int
	// StdoutTruncated / StderrTruncated report the tool-result caps.
	StdoutTruncated bool
	StderrTruncated bool
}

// ExecOptions are the identity and path options of an exec call (sync or
// async). User and Elevated are mutually exclusive: the default (both
// zero) runs as the logged-in guest user (--current-user); User selects
// --user <name>; Elevated passes neither flag, which makes prlctl run the
// command as the guest's system account (root / SYSTEM — to be verified on
// a real host, see spec 018). No password is ever accepted.
type ExecOptions struct {
	User         string
	Elevated     bool
	ResolvePaths bool
}

// Async exec job states.
const (
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
	JobCancelled = "cancelled"
	JobTimedOut  = "timed_out"
)

// ExecJobInfo is the outcome of StartExecJob.
type ExecJobInfo struct {
	JobID          string
	VM             string
	Elevated       bool
	TimeoutSeconds int
	StartedAt      time.Time
}

// ExecJobQuery selects what an ExecJobResult call returns: an optional
// long-poll wait and the stream offsets (bytes already consumed).
type ExecJobQuery struct {
	Wait         time.Duration
	StdoutOffset int64
	StderrOffset int64
}

// ExecJobStatus is a point-in-time view of an async exec job. Offsets are
// byte offsets into the retained output stream; Next* is where the
// following call should resume. The command text is never part of it.
type ExecJobStatus struct {
	JobID     string
	VM        string
	State     string
	Elevated  bool
	User      string
	ExitCode  int // -1 while running or when the process had no exit code
	Message   string
	ElapsedMS int64

	Stdout     string
	Stderr     string
	StdoutNext int64
	StderrNext int64
	// *Truncated: bytes beyond the per-stream retention cap were dropped.
	StdoutTruncated bool
	StderrTruncated bool
	// *HasMore: retained bytes remain beyond Next* (per-call cap hit).
	StdoutHasMore bool
	StderrHasMore bool
}

// ReclaimDiskSpace is the parsed `--estimate --json` disk-space report;
// all values are bytes.
type ReclaimDiskSpace struct {
	HardDisks     int64
	Snapshots     int64
	Miscellaneous int64
	Reclaimable   int64
}

// ReclaimDiskResult is the outcome of a reclaim-disk call. DiskSpace is
// set only for estimate runs.
type ReclaimDiskResult struct {
	Estimate  bool
	DiskSpace *ReclaimDiskSpace
	Output    string // prlctl's trimmed stdout
}

// SnapshotInfo is one entry of prlctl's snapshot list. IDs carry the
// curly braces exactly as prlctl reports them; Parent is "" for roots.
// Description is only populated by the single-snapshot list mode (the
// tree JSON carries no description).
type SnapshotInfo struct {
	ID          string
	Name        string // may be "" (created without -n)
	Date        string // "YYYY-MM-DD HH:MM:SS"
	State       string // "poweron" / "poweroff"
	Description string // single mode only
	Current     bool
	Parent      string
}

// SnapshotListResult is the outcome of a snapshot-list call. Exactly one
// of the modes applies: Snapshots (default tree JSON), Snapshot
// (single-snapshot mode; nil when prlctl found nothing), or Output
// (ASCII tree mode).
type SnapshotListResult struct {
	Snapshots []SnapshotInfo
	Snapshot  *SnapshotInfo
	NotFound  bool // single mode, bogus id: prlctl prints nothing, exit 0
	Output    string
}

// CloneRequest carries the clone options. Name is required; all other
// fields default to prlctl defaults when zero-valued.
type CloneRequest struct {
	Name              string // required: the new VM's name
	Template          bool   // --template
	Dst               string // --dst <path>; "" = prlctl default location
	RegenerateSrcUUID bool   // --regenerate-src-uuid
	Linked            bool   // --linked
	SnapshotID        string // -i, --id <snapid>; requires Linked
	DetachExternalHDD string // --detach-external-hdd <yes|no>; "" = unset
}

// ParallelsService is the contract the MCP tools consume. Implementations are
// expected to be singletons created via the parallels facade.
type ParallelsService interface {
	// Enabled reports whether prlctl and prlsrvctl were found at startup.
	Enabled() bool

	// VMID returns the configured target VM id or name (empty in fleet mode).
	VMID() string

	// PRLCtlPath / PRLSrvCtlPath return the resolved binary locations.
	PRLCtlPath() string
	PRLSrvCtlPath() string

	// ListVMs returns the cached VM list. In VM mode only the target VM is
	// returned. When the cache is empty or older than the TTL (or force is
	// true) the list command is re-run first.
	ListVMs(ctx context.Context, force bool) (VMListResult, error)

	// VMRaw returns the exact cached prlctl JSON blob of the target VM.
	// It is only meaningful in VM mode.
	VMRaw(ctx context.Context, force bool) (VMRawResult, error)

	// VMInfoRef returns the exact prlctl JSON blob of the VM identified by
	// ref (id or name, pre-validated), fetched live with
	// `prlctl list <ref> -a -i --json` — the same command as the list
	// fetch, scoped to one VM. It never reads or writes the cache.
	VMInfoRef(ctx context.Context, ref string) (VMRawResult, error)

	// Power executes a power command (op) against the VM identified by vmRef
	// (id or name, pre-validated). kill and dropState only apply to
	// PowerStop. On success the VM cache is invalidated. It returns
	// prlctl's stdout (usually empty on success).
	Power(ctx context.Context, op PowerOp, vmRef string, kill, dropState bool) (string, error)

	// Encrypt encrypts the VM identified by vmRef (id or name,
	// pre-validated) and all of its data behind password, or — when
	// dryRun is true — only checks the preconditions (prlctl reads no
	// password in dry-run). The password is fed to prlctl over stdin and
	// must never appear in argv, logs, or returned errors. On success the
	// VM cache is invalidated. Returns prlctl's stdout, trimmed.
	Encrypt(ctx context.Context, vmRef, password string, dryRun bool) (string, error)

	// Decrypt removes the encryption from the VM identified by vmRef. The
	// password must be the current one; a wrong password is a clean error
	// and leaves the VM encrypted. dryRun checks preconditions only.
	// Password handling and cache invalidation are as in Encrypt.
	Decrypt(ctx context.Context, vmRef, password string, dryRun bool) (string, error)

	// ChangePasswd changes the encryption password of the VM identified by
	// vmRef: prlctl reads two stdin lines — currentPassword then
	// newPassword, with no confirmation prompt. The VM must be encrypted.
	// Password handling and cache invalidation are as in Encrypt.
	ChangePasswd(ctx context.Context, vmRef, currentPassword, newPassword string) (string, error)

	// ReclaimDisk optimizes the VM's disk file to free host space
	// (prlctl reclaim-disk), or — when estimate is true — only reports
	// the estimated disk-space breakdown (prlctl's --estimate --json,
	// parsed into ReclaimDiskSpace; estimate is read-only and works on
	// running or stopped VMs). An encrypted VM requires password in both
	// modes; a real run on an encrypted VM is rejected by prlctl.
	// allowShutdown adds --allow-shutdown to a real run. A password, when
	// present, is fed over stdin (never argv/logs/errors). A real run
	// invalidates the VM cache; an estimate never does.
	ReclaimDisk(ctx context.Context, vmRef, password string, estimate, allowShutdown bool) (ReclaimDiskResult, error)

	// CreateSnapshot takes a VM snapshot (prlctl snapshot), optionally
	// named/described. Works on running and stopped VMs; the VM's state
	// is preserved. An encrypted VM requires password (one stdin line).
	// Returns the new snapshot id (parsed from prlctl's success message;
	// "" when it could not be parsed) and prlctl's trimmed stdout.
	CreateSnapshot(ctx context.Context, vmRef, name, description, password string) (id string, output string, err error)

	// DeleteSnapshot deletes a snapshot (prlctl snapshot-delete). With
	// children, all descendants are deleted too. Deleting the current
	// snapshot reverts the VM to its parent's state. An encrypted VM
	// requires password. Returns prlctl's trimmed stdout.
	DeleteSnapshot(ctx context.Context, vmRef, snapID, password string, children bool) (string, error)

	// ListSnapshots lists a VM's snapshots (prlctl snapshot-list). An
	// empty snapshotID lists the whole tree as JSON (empty tree → empty
	// Snapshots slice); a non-empty snapshotID selects the
	// single-snapshot mode (NotFound when the id is unknown — prlctl
	// prints nothing and exits 0 there); tree=true returns the ASCII
	// tree in Output. An encrypted VM requires password in all modes.
	ListSnapshots(ctx context.Context, vmRef, snapshotID, password string, tree bool) (SnapshotListResult, error)

	// SwitchSnapshot reverts the VM to a snapshot (prlctl snapshot-
	// switch). Without skipResume, a "poweron" snapshot starts the VM
	// afterwards; skipResume never starts it. Changes made after the
	// snapshot are lost from the live state. Works on running and
	// stopped VMs. An encrypted VM requires password. Invalidates the
	// VM cache (the only snapshot op that can flip the VM's State).
	SwitchSnapshot(ctx context.Context, vmRef, snapID, password string, skipResume bool) (string, error)

	// Capture takes a screenshot of the VM identified by vmRef (id or name,
	// pre-validated). prlctl can only write to a file, so the implementation
	// uses a private temp directory that is always removed before returning.
	// Capture does not change VM state and does not touch the VM list cache.
	Capture(ctx context.Context, vmRef string) (CaptureResult, error)

	// Exec runs command inside the VM identified by vmRef (id or name,
	// pre-validated). An empty user means --current-user; otherwise
	// --user <user> is used. Non-zero guest exit codes are results, not
	// errors: err is non-nil only when prlctl itself failed (unknown or
	// stopped VM, timeout, launch failure). Exec does not touch the VM
	// list cache.
	Exec(ctx context.Context, vmRef, command string, user string, resolvePaths bool) (ExecResult, error)

	// ExecWithOptions is Exec with the full option set (see ExecOptions),
	// notably Elevated. Exec(vm, cmd, user, rp) is equivalent to
	// ExecWithOptions(vm, cmd, ExecOptions{User: user, ResolvePaths: rp}).
	ExecWithOptions(ctx context.Context, vmRef, command string, opts ExecOptions) (ExecResult, error)

	// StartExecJob starts command in the VM as a background job and
	// returns immediately with its id. The job runs on a server-lifetime
	// context (not ctx), bounded by timeout (0 = 30 minutes, max 4
	// hours). At most 4 jobs run and 32 are retained at a time. The
	// command text is never stored or logged.
	StartExecJob(ctx context.Context, vmRef, command string, opts ExecOptions, timeout time.Duration) (ExecJobInfo, error)

	// ExecJobResult returns the state and the next output slices of a job
	// (optionally long-polling up to q.Wait for completion). In VM mode
	// only jobs of the configured target are visible.
	ExecJobResult(ctx context.Context, jobID string, q ExecJobQuery) (ExecJobStatus, error)

	// CancelExecJob kills a running job (no-op on a finished one) and
	// reports its status (no output). wasRunning says whether it was still
	// running when the call arrived.
	CancelExecJob(ctx context.Context, jobID string) (status ExecJobStatus, wasRunning bool, err error)

	// SendKey sends a sequence of keyboard events to the VM identified by
	// vmRef (a VM id or name, pre-validated) by running
	// `prlctl send-key-event <vm> -j` with the serialized events on
	// stdin. There is no guest exit-code concept here: any prlctl
	// failure is an error carrying its stderr. The event content is
	// never included in error values or logs. SendKey does not touch
	// the VM list cache.
	SendKey(ctx context.Context, vmRef string, events []KeyEvent) (string, error)

	// Delete permanently removes the VM identified by vmRef (a VM id or
	// name, pre-validated) and all its files from the host via
	// `prlctl delete <vm>`. The operation is irreversible. On success
	// the VM list cache is invalidated. Any prlctl failure is an error
	// carrying its stderr.
	Delete(ctx context.Context, vmRef string) (string, error)

	// Clone creates a copy of the VM identified by vmRef (a VM id or
	// name, pre-validated) named req.Name via `prlctl clone`. The
	// source VM is never modified. On success the VM list cache is
	// invalidated because the list gained a VM. Any prlctl failure is
	// an error carrying its stderr.
	Clone(ctx context.Context, vmRef string, req CloneRequest) (string, error)

	// RefreshCache re-runs the list command unconditionally and replaces the
	// cache contents.
	RefreshCache(ctx context.Context) error

	// InvalidateCache marks the whole cache as stale (call after a
	// command that mutates VM state).
	InvalidateCache()
}
