package builtin

// volume.go is the WASM VolumeDriver scaffold. Wasm workloads are
// ephemeral by default ; persistence (pin-to-edge via weft-block
// reflinks) lands in V0.2 of the design doc.

import (
	"context"

	drivers "github.com/openweft/weft-drivers"
)

// Volume implements drivers.VolumeDriver for WASM hosts.
type Volume struct {
	opts Options
}

func NewVolume(o Options) *Volume { return &Volume{opts: o} }

var _ drivers.VolumeDriver = (*Volume)(nil)

func (v *Volume) Name() string { return "wasm-ephemeral" }
func (v *Volume) Local() bool  { return true }
func (v *Volume) HostInfo(context.Context) (drivers.HostInfo, error) {
	return hostInfoFor(v.opts), nil
}
func (v *Volume) EnsureVolume(context.Context, drivers.VolumeSpec) error {
	return drivers.ErrUnsupported
}
func (v *Volume) DestroyVolume(context.Context, string) error { return drivers.ErrUnsupported }
func (v *Volume) AttachVolume(context.Context, string, string) (drivers.AttachedVolume, error) {
	return drivers.AttachedVolume{}, drivers.ErrUnsupported
}
func (v *Volume) DetachVolume(context.Context, string, string) error { return drivers.ErrUnsupported }
func (v *Volume) CreateSnapshot(context.Context, drivers.SnapshotSpec) (drivers.Snapshot, error) {
	return drivers.Snapshot{}, drivers.ErrUnsupported
}
func (v *Volume) ListSnapshots(context.Context, string) ([]drivers.Snapshot, error) {
	return nil, drivers.ErrUnsupported
}
func (v *Volume) DeleteSnapshot(context.Context, string, string) error {
	return drivers.ErrUnsupported
}
func (v *Volume) RevertSnapshot(context.Context, string, string) error {
	return drivers.ErrUnsupported
}
func (v *Volume) CreateBackup(context.Context, drivers.BackupSpec) (drivers.Backup, error) {
	return drivers.Backup{}, drivers.ErrUnsupported
}
func (v *Volume) ListBackups(context.Context, string, string) ([]drivers.Backup, error) {
	return nil, drivers.ErrUnsupported
}
func (v *Volume) DeleteBackup(context.Context, string) error {
	return drivers.ErrUnsupported
}
func (v *Volume) RestoreBackup(context.Context, string, drivers.VolumeSpec) error {
	return drivers.ErrUnsupported
}
