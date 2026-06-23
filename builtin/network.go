package builtin

// network.go is the WASM NetworkDriver scaffold. Wasm workloads
// don't get L2 ; the V0.2 outbound-network plan routes through a
// per-agent virtual L3 endpoint exposed to the module via WASI
// http capabilities. Until that lands the four lifecycle verbs
// return ErrUnsupported so the agent sees a clean "this backend
// doesn't manage networks".

import (
	"context"

	drivers "github.com/openweft/weft-drivers"
)

// Network implements drivers.NetworkDriver for WASM hosts.
type Network struct {
	opts Options
}

func NewNetwork(o Options) *Network { return &Network{opts: o} }

var _ drivers.NetworkDriver = (*Network)(nil)

func (n *Network) HostInfo(context.Context) (drivers.HostInfo, error) {
	return hostInfoFor(n.opts), nil
}
func (n *Network) EnsureNetwork(context.Context, drivers.NetworkSpec) error {
	return drivers.ErrUnsupported
}
func (n *Network) DestroyNetwork(context.Context, string) error { return drivers.ErrUnsupported }
func (n *Network) AttachPort(context.Context, drivers.PortSpec) (drivers.NICHandle, error) {
	return drivers.NICHandle{}, drivers.ErrUnsupported
}
func (n *Network) DetachPort(context.Context, string) error { return drivers.ErrUnsupported }
func (n *Network) RotateMeshPeer(context.Context, drivers.PortSpec) error {
	return drivers.ErrUnsupported
}
