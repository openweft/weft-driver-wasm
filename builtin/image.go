package builtin

// image.go is the WASM ImageDriver scaffold. Image caching for wasm
// modules (OCI artifact, mediatype application/wasm) is shared
// platform logic that will be wrapped here once the imagestore is
// factored out of weft ; for now it reports nothing cached and the
// Hypervisor.fetchModule path covers V0.1 needs.

import (
	"context"

	drivers "github.com/openweft/weft-drivers"
)

// Image implements drivers.ImageDriver for WASM hosts.
type Image struct {
	opts Options
}

func NewImage(o Options) *Image { return &Image{opts: o} }

var _ drivers.ImageDriver = (*Image)(nil)

func (i *Image) HostInfo(context.Context) (drivers.HostInfo, error) {
	return hostInfoFor(i.opts), nil
}
func (i *Image) Pull(context.Context, string) error                { return drivers.ErrUnsupported }
func (i *Image) LocalPath(context.Context, string) (string, error) { return "", drivers.ErrUnsupported }
func (i *Image) Delete(context.Context, string) error              { return drivers.ErrUnsupported }
func (i *Image) InCache(context.Context, string) (bool, error)     { return false, nil }
