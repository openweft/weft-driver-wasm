package builtin

// bundle.go assembles the four driver instances a weft agent needs
// to run on a WASM host. Mirrors weft-driver-qemu's Bundle so the
// agent's dispatch wiring is identical regardless of backend.

// Bundle holds the four WASM-host driver instances.
type Bundle struct {
	Hypervisor *Hypervisor
	Network    *Network
	Volume     *Volume
	Image      *Image
}

// BundleOptions wraps construction inputs for all four drivers.
type BundleOptions struct {
	Options
}

// New returns the driver bundle for one WASM host ; all four
// drivers share the same HostInfo.
func New(o BundleOptions) *Bundle {
	return &Bundle{
		Hypervisor: NewHypervisor(o.Options),
		Network:    NewNetwork(o.Options),
		Volume:     NewVolume(o.Options),
		Image:      NewImage(o.Options),
	}
}
