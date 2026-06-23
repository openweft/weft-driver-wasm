package builtin

// host_info.go renders the static HostInfo every wasm-driver
// service returns. Mirrors weft-driver-qemu/builtin/host_info.go
// so the agent's introspection logic doesn't special-case kinds.

import (
	"runtime"

	drivers "github.com/openweft/weft-drivers"
)

// hostInfoFor builds a HostInfo for the wasm backend. Hypervisor
// = "wasm" so the scheduler + audit logs can tell at a glance which
// backend served a side effect — same shape weft-driver-qemu /
// weft-driver-vz return.
func hostInfoFor(opts Options) drivers.HostInfo {
	return drivers.HostInfo{
		UUID:         opts.HostUUID,
		Hostname:     opts.Hostname,
		Hypervisor:   "wasm",
		Architecture: runtime.GOARCH,
		Version:      Version,
	}
}

// Version is the driver plugin's compile-time build version.
// Overridable via `-ldflags "-X .../builtin.Version=vX.Y.Z"` from
// the cmd/weft-driver-wasm main package.
var Version = "v0.1.0-dev"
