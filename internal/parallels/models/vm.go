// Package models defines the data shapes returned by the Parallels CLI.
// JSON tags must match prlctl's exact key spelling; keys not modelled here
// are dropped by the typed decode but preserved in the raw cache blob.
package models

// VM is one element of `prlctl list -a -i --json`.
type VM struct {
	ID                    string               `json:"ID"`
	Name                  string               `json:"Name"`
	Description           string               `json:"Description"`
	Type                  string               `json:"Type"`
	State                 string               `json:"State"`
	OS                    string               `json:"OS"`
	Template              string               `json:"Template"`
	Uptime                string               `json:"Uptime"`
	HomePath              string               `json:"Home path"`
	Home                  string               `json:"Home"`
	RestoreImage          string               `json:"Restore Image"`
	GuestTools            GuestTools           `json:"GuestTools"`
	MouseAndKeyboard      MouseAndKeyboard     `json:"Mouse and Keyboard"`
	USBAndBluetooth       USBAndBluetooth      `json:"USB and Bluetooth"`
	StartupAndShutdown    StartupAndShutdown   `json:"Startup and Shutdown"`
	Optimization          Optimization         `json:"Optimization"`
	Security              Security             `json:"Security"`
	SmartGuard            Toggle               `json:"Smart Guard"`
	Modality              Modality             `json:"Modality"`
	Fullscreen            Fullscreen           `json:"Fullscreen"`
	Coherence             Coherence            `json:"Coherence"`
	TimeSynchronization   TimeSynchronization  `json:"Time Synchronization"`
	Expiration            Toggle               `json:"Expiration"`
	BootOrder             string               `json:"Boot order"`
	BIOSType              string               `json:"BIOS type"`
	EFISecureBoot         string               `json:"EFI Secure boot"`
	AllowSelectBootDevice string               `json:"Allow select boot device"`
	ExternalBootDevice    string               `json:"External boot device"`
	SMBIOSSettings        SMBIOSSettings       `json:"SMBIOS settings"`
	Hardware              Hardware             `json:"Hardware"`
	HostSharedFolders     Toggle               `json:"Host Shared Folders"`
	HostDefinedSharing    string               `json:"Host defined sharing"`
	SharedProfile         Toggle               `json:"Shared Profile"`
	SharedApplications    SharedApplications   `json:"Shared Applications"`
	SmartMount            SmartMount           `json:"SmartMount"`
	Network               Network              `json:"Network"`
	MiscellaneousSharing  MiscellaneousSharing `json:"Miscellaneous Sharing"`
	Advanced              Advanced             `json:"Advanced"`
}

// Normalize fills nil slices so the JSON encoding never emits null for
// fields whose output schema declares a bare "array" type (prlctl emits
// empty arrays, but a stray null must not leak into tool output).
func (v *VM) Normalize() {
	if v.Network.IPAddresses == nil {
		v.Network.IPAddresses = []NetworkIPAddress{}
	}
}

// Toggle is the minimal shape of booleans prlctl reports as {"enabled": bool}.
type Toggle struct {
	Enabled bool `json:"enabled"`
}

// GuestTools mirrors the `GuestTools` block of prlctl output.
type GuestTools struct {
	State   string `json:"state"`
	Version string `json:"version,omitempty"`
}

// MouseAndKeyboard mirrors the `Mouse and Keyboard` block.
type MouseAndKeyboard struct {
	SmartMouseOptimizedForGames string `json:"Smart mouse optimized for games"`
	StickyMouse                 string `json:"Sticky mouse"`
	SmoothScrolling             string `json:"Smooth scrolling"`
	KeyboardOptimizationMode    string `json:"Keyboard optimization mode"`
}

// USBAndBluetooth mirrors the `USB and Bluetooth` block.
type USBAndBluetooth struct {
	AutomaticSharingCameras    string `json:"Automatic sharing cameras"`
	AutomaticSharingBluetooth  string `json:"Automatic sharing bluetooth"`
	AutomaticSharingSmartCards string `json:"Automatic sharing smart cards"`
	AutomaticSharingGamepads   string `json:"Automatic sharing gamepads"`
	SupportUSB30               string `json:"Support USB 3.0"`
}

// StartupAndShutdown mirrors the `Startup and Shutdown` block.
type StartupAndShutdown struct {
	Autostart      string `json:"Autostart"`
	AutostartDelay int64  `json:"Autostart delay"`
	Autostop       string `json:"Autostop"`
	StartupView    string `json:"Startup view"`
	OnShutdown     string `json:"On shutdown"`
	OnWindowClose  string `json:"On window close"`
	PauseIdle      string `json:"Pause idle"`
	UndoDisks      string `json:"Undo disks"`
}

// Optimization mirrors the `Optimization` block.
type Optimization struct {
	FasterVirtualMachine     string `json:"Faster virtual machine"`
	HypervisorType           string `json:"Hypervisor type"`
	AdaptiveHypervisor       string `json:"Adaptive hypervisor"`
	DisabledWindowsLogo      string `json:"Disabled Windows logo"`
	AutoCompressVirtualDisks string `json:"Auto compress virtual disks"`
	NestedVirtualization     string `json:"Nested virtualization"`
	PMUVirtualization        string `json:"PMU virtualization"`
	LongerBatteryLife        string `json:"Longer battery life"`
	ShowBatteryStatus        string `json:"Show battery status"`
}

// Security mirrors the `Security` block.
type Security struct {
	Encrypted  string `json:"Encrypted"`
	TPMEnabled string `json:"TPM enabled"`
	TPMType    string `json:"TPM type"`
	Protected  string `json:"Protected"`
	Archived   string `json:"Archived"`
	Packed     string `json:"Packed"`
}

// Modality mirrors the `Modality` block.
type Modality struct {
	OpacityPercentage  int64  `json:"Opacity (percentage)"`
	StayOnTop          string `json:"Stay on top"`
	ShowOnAllSpaces    string `json:"Show on all spaces "`
	CaptureMouseClicks string `json:"Capture mouse clicks"`
}

// Fullscreen mirrors the `Fullscreen` block.
type Fullscreen struct {
	UseAllDisplays        string `json:"Use all displays"`
	ActivateSpacesOnClick string `json:"Activate spaces on click"`
	OptimizeForGames      string `json:"Optimize for games"`
	GammaControl          string `json:"Gamma control"`
	ScaleViewMode         string `json:"Scale view mode"`
}

// Coherence mirrors the `Coherence` block.
type Coherence struct {
	ShowWindowsSystrayInMACMenu string `json:"Show Windows systray in Mac menu"`
	AutoSwitchToFullScreen      string `json:"Auto-switch to full screen"`
}

// TimeSynchronization mirrors the `Time Synchronization` block.
type TimeSynchronization struct {
	Enabled                         bool   `json:"enabled"`
	SmartMode                       string `json:"Smart mode"`
	IntervalInSeconds               int64  `json:"Interval (in seconds)"`
	TimezoneSynchronizationDisabled string `json:"Timezone synchronization disabled"`
}

// SMBIOSSettings mirrors the `SMBIOS settings` block.
type SMBIOSSettings struct {
	BIOSVersion        string `json:"BIOS Version"`
	SystemSerialNumber string `json:"System serial number"`
	BoardManufacturer  string `json:"Board Manufacturer"`
}

// Hardware carries the device configuration reported by prlctl.
type Hardware struct {
	CPU         CPU         `json:"cpu"`
	Memory      Memory      `json:"memory"`
	Video       Video       `json:"video"`
	MemoryQuota MemoryQuota `json:"memory_quota"`
	Hdd0        Hdd0        `json:"hdd0"`
	Cdrom0      Cdrom0      `json:"cdrom0"`
	USB         Toggle      `json:"usb"`
	Net0        Net0        `json:"net0"`
	Sound0      Sound0      `json:"sound0"`
}

// CPU mirrors the `Hardware.cpu` block.
type CPU struct {
	Cpus    int64  `json:"cpus"`
	Auto    string `json:"auto"`
	VTX     bool   `json:"VT-x"`
	Hotplug bool   `json:"hotplug"`
	Accl    string `json:"accl"`
	Mode    string `json:"mode"`
	Type    string `json:"type"`
}

// Memory mirrors the `Hardware.memory` block.
type Memory struct {
	Size string `json:"size"`
	Auto string `json:"auto"`
}

// MemoryQuota mirrors the `Hardware.memory_quota` block.
type MemoryQuota struct {
	Auto string `json:"auto"`
}

// Video mirrors the `Hardware.video` block.
type Video struct {
	AdapterType           string `json:"adapter-type"`
	Size                  string `json:"size"`
	The3DAcceleration     string `json:"3d-acceleration"`
	VerticalSync          string `json:"vertical-sync"`
	HighResolution        string `json:"high-resolution"`
	HighResolutionInGuest string `json:"high-resolution-in-guest"`
	NativeScalingInGuest  string `json:"native-scaling-in-guest"`
	AutomaticVideoMemory  string `json:"automatic-video-memory"`
}

// Hdd0 mirrors the `Hardware.hdd0` block.
type Hdd0 struct {
	Enabled       bool   `json:"enabled"`
	Port          string `json:"port"`
	Image         string `json:"image"`
	Type          string `json:"type"`
	Size          string `json:"size"`
	OnlineCompact string `json:"online-compact"`
}

// Cdrom0 mirrors the `Hardware.cdrom0` block.
type Cdrom0 struct {
	Enabled bool   `json:"enabled"`
	Port    string `json:"port"`
	Image   string `json:"image"`
	State   string `json:"state,omitempty"`
}

// Net0 mirrors the `Hardware.net0` block.
type Net0 struct {
	Enabled bool   `json:"enabled"`
	Type    string `json:"type"`
	MAC     string `json:"mac"`
	Card    string `json:"card"`
}

// Sound0 mirrors the `Hardware.sound0` block.
type Sound0 struct {
	Enabled bool   `json:"enabled"`
	Output  string `json:"output"`
	Mixer   string `json:"mixer"`
}

// SharedApplications mirrors the `Shared Applications` block.
type SharedApplications struct {
	Enabled                      bool   `json:"enabled"`
	HostToGuestAppsSharing       string `json:"Host-to-guest apps sharing"`
	GuestToHostAppsSharing       string `json:"Guest-to-host apps sharing"`
	ShowGuestAppsFolderInDock    string `json:"Show guest apps folder in Dock"`
	BounceDockIconWhenAppFlashes string `json:"Bounce dock icon when app flashes"`
}

// SmartMount mirrors the `SmartMount` block.
type SmartMount struct {
	Enabled         bool   `json:"enabled"`
	RemovableDrives string `json:"Removable drives"`
	CDDVDDrives     string `json:"CD/DVD drives"`
	NetworkShares   string `json:"Network shares"`
}

// Network mirrors the `Network` block.
type Network struct {
	Conditioned string             `json:"Conditioned"`
	IPAddresses []NetworkIPAddress `json:"ipAddresses"`
	Inbound     NetworkBound       `json:"Inbound,omitempty"`
	Outbound    NetworkBound       `json:"Outbound,omitempty"`
}

// NetworkIPAddress is one entry of the `ipAddresses` array.
type NetworkIPAddress struct {
	Type string `json:"type"`
	IP   string `json:"ip"`
}

// NetworkBound mirrors the `Inbound`/`Outbound` QoS blocks.
type NetworkBound struct {
	Bandwidth  string `json:"Bandwidth"`
	PacketLoss string `json:"Packet Loss"`
	Delay      string `json:"Delay"`
}

// MiscellaneousSharing mirrors the `Miscellaneous Sharing` block.
type MiscellaneousSharing struct {
	SharedClipboardMode string `json:"Shared clipboard mode"`
	SharedCloud         string `json:"Shared cloud"`
}

// Advanced mirrors the `Advanced` block.
type Advanced struct {
	VMHostnameSynchronization    string `json:"VM hostname synchronization"`
	PublicSSHKeysSynchronization string `json:"Public SSH keys synchronization"`
	ShowDeveloperTools           string `json:"Show developer tools"`
	SwipeFromEdges               string `json:"Swipe from edges"`
	ShareHostLocation            string `json:"Share host location"`
	RosettaLinux                 string `json:"Rosetta Linux"`
}
