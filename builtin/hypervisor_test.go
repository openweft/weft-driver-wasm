package builtin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	drivers "github.com/openweft/weft-drivers"
)

// minimalWasmNoop is the binary form of `(module (func (export
// "_start")))` — a WASI command module whose `_start` returns
// immediately. Verifies the Create→Start→Stop→Delete path without
// pulling a real fixture or compiling Rust in the test ; the bytes
// are stable across wazero versions (wasm spec frozen).
var minimalWasmNoop = []byte{
	0x00, 0x61, 0x73, 0x6d, // magic
	0x01, 0x00, 0x00, 0x00, // version 1
	// Type section : 1 type, () -> ()
	0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
	// Function section : 1 function of type 0
	0x03, 0x02, 0x01, 0x00,
	// Export section : "_start" → func 0
	0x07, 0x0a, 0x01, 0x06, '_', 's', 't', 'a', 'r', 't', 0x00, 0x00,
	// Code section : 1 function, body = "end"
	0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b,
}

func TestHypervisor_CreateStartStopDelete_LocalFile(t *testing.T) {
	dir := t.TempDir()
	modPath := filepath.Join(dir, "noop.wasm")
	if err := os.WriteFile(modPath, minimalWasmNoop, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	h := NewHypervisor(Options{
		HostUUID: "test-host",
		Hostname: "test",
	})

	vmDir := filepath.Join(dir, "vm-1")
	spec := drivers.VMSpec{
		UUID:     vmDir,
		Name:     "vm-1",
		BootKind: "oci_image",
		BootRef:  modPath,
	}
	ctx := context.Background()

	if err := h.CreateVM(ctx, spec); err != nil {
		t.Fatalf("CreateVM: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vmDir, "module.wasm")); err != nil {
		t.Fatalf("module.wasm not created : %v", err)
	}

	// Idempotence : second Create should be a no-op.
	if err := h.CreateVM(ctx, spec); err != nil {
		t.Fatalf("CreateVM idempotent: %v", err)
	}

	if err := h.StartVM(ctx, vmDir); err != nil {
		t.Fatalf("StartVM: %v", err)
	}
	// The noop module returns immediately ; give it a beat then
	// stop is a no-op.
	time.Sleep(150 * time.Millisecond)

	stopCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := h.StopVM(stopCtx, vmDir); err != nil {
		t.Fatalf("StopVM: %v", err)
	}

	if err := h.DeleteVM(ctx, vmDir); err != nil {
		t.Fatalf("DeleteVM: %v", err)
	}
	if _, err := os.Stat(vmDir); !os.IsNotExist(err) {
		t.Fatalf("vmDir should be gone after delete : %v", err)
	}
	// Idempotence : Delete on a missing VM is fine.
	if err := h.DeleteVM(ctx, vmDir); err != nil {
		t.Fatalf("DeleteVM idempotent: %v", err)
	}
}

func TestHypervisor_FetchModule_HTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, ".wasm") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(minimalWasmNoop)
	}))
	defer srv.Close()

	h := NewHypervisor(Options{HostUUID: "h", Hostname: "h"})
	dir := t.TempDir()
	vmDir := filepath.Join(dir, "vm-http")
	spec := drivers.VMSpec{
		UUID:    vmDir,
		BootRef: srv.URL + "/m.wasm",
	}
	if err := h.CreateVM(context.Background(), spec); err != nil {
		t.Fatalf("CreateVM HTTP: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(vmDir, "module.wasm"))
	if err != nil {
		t.Fatalf("read module: %v", err)
	}
	if len(got) != len(minimalWasmNoop) {
		t.Fatalf("size mismatch : got %d want %d", len(got), len(minimalWasmNoop))
	}
}

func TestHypervisor_FetchModule_FileURL(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.wasm")
	if err := os.WriteFile(src, minimalWasmNoop, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	h := NewHypervisor(Options{HostUUID: "h", Hostname: "h"})
	vmDir := filepath.Join(dir, "vm-file")
	spec := drivers.VMSpec{
		UUID: vmDir,
		// The documented form is file:///abs/path -- THREE slashes. Writing
		// "file://"+src produced that on unix only by accident, because src
		// starts with a separator there; on Windows src is C:\... and the
		// result had two, so url.Parse read "C:" as host:port and rejected it.
		BootRef: fileURL(src),
	}
	if err := h.CreateVM(context.Background(), spec); err != nil {
		t.Fatalf("CreateVM file URL: %v", err)
	}
}

func TestHypervisor_OCIRefUnsupported(t *testing.T) {
	h := NewHypervisor(Options{HostUUID: "h", Hostname: "h"})
	dir := t.TempDir()
	vmDir := filepath.Join(dir, "vm-oci")
	spec := drivers.VMSpec{
		UUID:    vmDir,
		BootRef: "oci://ghcr.io/openweft/wasm-hello:v0",
	}
	err := h.CreateVM(context.Background(), spec)
	if err == nil {
		t.Fatalf("CreateVM oci:// should fail until V0.2")
	}
}

func TestHypervisor_MaxConcurrent(t *testing.T) {
	dir := t.TempDir()
	modPath := filepath.Join(dir, "noop.wasm")
	if err := os.WriteFile(modPath, minimalWasmNoop, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	h := NewHypervisor(Options{
		HostUUID:         "h",
		Hostname:         "h",
		MaxConcurrentVMs: 1,
	})

	// Create two VMs sharing the same module.
	mkVM := func(name string) string {
		vmDir := filepath.Join(dir, name)
		if err := h.CreateVM(context.Background(), drivers.VMSpec{
			UUID:    vmDir,
			BootRef: modPath,
		}); err != nil {
			t.Fatalf("CreateVM %s: %v", name, err)
		}
		return vmDir
	}
	a := mkVM("a")
	b := mkVM("b")

	if err := h.StartVM(context.Background(), a); err != nil {
		t.Fatalf("StartVM a: %v", err)
	}
	// The noop module returns immediately, but the cleanup
	// goroutine may not have run yet — give it a beat for the
	// concurrent-capacity check to land.
	time.Sleep(50 * time.Millisecond)
	// b may or may not race the cleanup — exercise both branches.
	if err := h.StartVM(context.Background(), b); err != nil {
		// Capacity hit is the expected outcome on a slow runner.
		if !strings.Contains(err.Error(), "at capacity") {
			t.Fatalf("StartVM b unexpected err: %v", err)
		}
	}
}

func TestHypervisor_HostInfo(t *testing.T) {
	h := NewHypervisor(Options{HostUUID: "abc", Hostname: "node-1"})
	info, err := h.HostInfo(context.Background())
	if err != nil {
		t.Fatalf("HostInfo: %v", err)
	}
	if info.Hypervisor != "wasm" {
		t.Errorf("Hypervisor=%q want wasm", info.Hypervisor)
	}
	if info.UUID != "abc" {
		t.Errorf("UUID=%q want abc", info.UUID)
	}
}

func TestHypervisor_AttachDetach_NoOp(t *testing.T) {
	h := NewHypervisor(Options{HostUUID: "h"})
	ctx := context.Background()
	if err := h.AttachDisk(ctx, "x", drivers.DiskSpec{}); err != nil {
		t.Errorf("AttachDisk: %v", err)
	}
	if err := h.DetachDisk(ctx, "x", "y"); err != nil {
		t.Errorf("DetachDisk: %v", err)
	}
	if err := h.AttachNIC(ctx, "x", drivers.NICHandle{}); err != nil {
		t.Errorf("AttachNIC: %v", err)
	}
	if err := h.DetachNIC(ctx, "x", "eth0"); err != nil {
		t.Errorf("DetachNIC: %v", err)
	}
}

// fileURL builds the file:///abs/path form this package documents, from a path
// in whatever shape the running OS uses.
func fileURL(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // a Windows path starts at its volume: C:/x -> /C:/x
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// filePathFromURL is exercised for BOTH operating systems from one machine,
// because a conversion tested only where it already works is not tested.
func TestFilePathFromURL(t *testing.T) {
	for _, tc := range []struct {
		name, raw, goos, want string
	}{
		{"unix", "file:///tmp/x.wasm", "linux", "/tmp/x.wasm"},
		{"windows volume", "file:///C:/dir/x.wasm", "windows", "C:/dir/x.wasm"},
		// The same URL under a non-Windows goos keeps its leading slash: this
		// is what proves the parameter is doing anything at all.
		{"volume-shaped URL, but not windows", "file:///C:/dir/x.wasm", "linux", "/C:/dir/x.wasm"},
		// No volume letter, so nothing to strip even on windows.
		{"windows, no volume", "file:///host/share/x.wasm", "windows", "/host/share/x.wasm"},
		{"windows, too short to hold a volume", "file:///", "windows", "/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got := filePathFromURL(u, tc.goos); got != tc.want {
				t.Errorf("filePathFromURL(%q, %q) = %q, want %q", tc.raw, tc.goos, got, tc.want)
			}
		})
	}
}
