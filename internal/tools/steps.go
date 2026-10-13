package tools

import (
	"github.com/Parallels/pdfm-mcp/internal/orchestrator"
)

// RegisterSteps registers every parallels_vm_* tool — except
// parallels_vm_info (no-arg read) and parallels_vm_sequence (the orchestrator
// itself) — as an orchestrator action. The input schemas are the VM-mode
// schemas: step args never carry vm_id (the runner injects it in fleet
// mode), so the schema drives the generated description without it.
func RegisterSteps(reg *orchestrator.Registry) {
	reg.Register(orchestrator.FromHandler[PowerVMInput, PowerOutput](
		"parallels_vm_start", descVMStart, true,
		powerInputSchema[PowerVMInput](PowerVM), VMStart))
	reg.Register(orchestrator.FromHandler[PowerVMInput, PowerOutput](
		"parallels_vm_resume", descVMResume, true,
		powerInputSchema[PowerVMInput](PowerVM), VMResume))
	reg.Register(orchestrator.FromHandler[PowerVMInput, PowerOutput](
		"parallels_vm_pause", descVMPause, true,
		powerInputSchema[PowerVMInput](PowerVM), VMPause))
	reg.Register(orchestrator.FromHandler[PowerVMInput, PowerOutput](
		"parallels_vm_suspend", descVMSuspend, true,
		powerInputSchema[PowerVMInput](PowerVM), VMSuspend))
	reg.Register(orchestrator.FromHandler[PowerVMInput, PowerOutput](
		"parallels_vm_restart", descVMRestart, true,
		powerInputSchema[PowerVMInput](PowerVM), VMRestart))
	reg.Register(orchestrator.FromHandler[PowerVMInput, PowerOutput](
		"parallels_vm_reset", descVMReset, true,
		powerInputSchema[PowerVMInput](PowerVM), VMReset))
	reg.Register(orchestrator.FromHandler[PowerVMInput, PowerOutput](
		"parallels_vm_reset_uptime", descVMResetUptime, true,
		powerInputSchema[PowerVMInput](PowerVM), VMResetUptime))
	reg.Register(orchestrator.FromHandler[PowerStopInput, PowerOutput](
		"parallels_vm_stop", descVMStop, true,
		powerInputSchema[PowerStopInput](PowerVM), VMStop))
	reg.Register(orchestrator.FromHandler[ExecInput, ExecOutput](
		"parallels_vm_exec", descVMExec, true,
		powerInputSchema[ExecInput](PowerVM), VMExec))
	reg.Register(orchestrator.FromHandler[SendKeyInput, SendKeyOutput](
		"parallels_vm_send_key", descVMSendKey, true,
		powerInputSchema[SendKeyInput](PowerVM), VMSendKey))
	reg.Register(orchestrator.FromHandler[TypeTextInput, TypeTextOutput](
		"parallels_vm_type", descVMTypeText, true,
		powerInputSchema[TypeTextInput](PowerVM), VMTypeText))
	reg.Register(orchestrator.FromHandler[DeleteInput, DeleteOutput](
		"parallels_vm_delete", descVMDelete, true,
		powerInputSchema[DeleteInput](PowerVM), VMDelete))
	reg.Register(orchestrator.FromHandler[CloneInput, CloneOutput](
		"parallels_vm_clone", descVMClone, true,
		powerInputSchema[CloneInput](PowerVM), VMClone))

	reg.Register(orchestrator.FromHandler[VMEncryptInput, VMEncryptDecryptOutput](
		"parallels_vm_encrypt", descVMEncrypt, true,
		powerInputSchema[VMEncryptInput](PowerVM), VMEncrypt))
	reg.Register(orchestrator.FromHandler[VMDecryptInput, VMEncryptDecryptOutput](
		"parallels_vm_decrypt", descVMDecrypt, true,
		powerInputSchema[VMDecryptInput](PowerVM), VMDecrypt))
	reg.Register(orchestrator.FromHandler[VMChangeEncryptionPasswordInput, VMChangeEncryptionPasswordOutput](
		"parallels_vm_change_encryption_password", descVMChangeEncryptionPassword, true,
		powerInputSchema[VMChangeEncryptionPasswordInput](PowerVM), VMChangeEncryptionPassword))

	reg.Register(orchestrator.FromHandler[VMReclaimDiskInput, VMReclaimDiskOutput](
		"parallels_vm_reclaim_disk", descVMReclaimDisk, true,
		powerInputSchema[VMReclaimDiskInput](PowerVM), VMReclaimDisk))

	reg.Register(orchestrator.FromHandler[VMSetInput, VMSetOutput](
		"parallels_vm_set", descVMSet, true,
		powerInputSchema[VMSetInput](PowerVM), VMSet))

	reg.Register(orchestrator.FromHandler[VMDeviceAddInput, VMDeviceOutput](
		"parallels_vm_device_add", descVMDeviceAdd, true,
		powerInputSchema[VMDeviceAddInput](PowerVM), VMDeviceAdd))
	reg.Register(orchestrator.FromHandler[VMDeviceSetInput, VMDeviceOutput](
		"parallels_vm_device_set", descVMDeviceSet, true,
		powerInputSchema[VMDeviceSetInput](PowerVM), VMDeviceSet))
	reg.Register(orchestrator.FromHandler[VMDeviceDelInput, VMDeviceOutput](
		"parallels_vm_device_del", descVMDeviceDel, true,
		powerInputSchema[VMDeviceDelInput](PowerVM), VMDeviceDel))

	reg.Register(orchestrator.FromHandler[VMCreateSnapshotInput, VMCreateSnapshotOutput](
		"parallels_vm_snapshot_create", descVMSnapshotCreate, true,
		powerInputSchema[VMCreateSnapshotInput](PowerVM), VMCreateSnapshot))
	reg.Register(orchestrator.FromHandler[VMListSnapshotsInput, VMListSnapshotsOutput](
		"parallels_vm_snapshot_list", descVMSnapshotList, true,
		powerInputSchema[VMListSnapshotsInput](PowerVM), VMListSnapshots))
	reg.Register(orchestrator.FromHandler[VMDeleteSnapshotInput, VMDeleteSnapshotOutput](
		"parallels_vm_snapshot_delete", descVMSnapshotDelete, true,
		powerInputSchema[VMDeleteSnapshotInput](PowerVM), VMDeleteSnapshot))
	reg.Register(orchestrator.FromHandler[VMSwitchSnapshotInput, VMSwitchSnapshotOutput](
		"parallels_vm_snapshot_switch", descVMSnapshotSwitch, true,
		powerInputSchema[VMSwitchSnapshotInput](PowerVM), VMSwitchSnapshot))

	capture := orchestrator.FromHandler[CaptureInput, CaptureOutput](
		"parallels_vm_capture", descVMCapture, true,
		powerInputSchema[CaptureInput](PowerVM), VMCapture)
	capture.ProducesImages = true
	reg.Register(capture)
}
