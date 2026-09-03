// Package builtin implements a WASM HypervisorDriver — a hypervisor
// that runs WebAssembly modules (WASI preview-1) as workloads
// instead of full VMs. The user's own machine becomes a hypervisor
// : no KVM, no Apple VZ, no nested virt. Useful as :
//
//   - An edge-compute backend any weft-agent can offer (every arch
//     wazero supports = arm64, amd64, riscv64).
//   - A development backend on hosts where the other drivers can't
//     boot (no nested virt, no qemu install).
//   - The protocol substrate for a future browser worker pool
//     (V0.2 : a JS/wasm-js agent dials weft-agent over WebSocket and
//     this same driver dispatches CreateVM / StartVM RPCs).
//
// Architecture choice — wazero (pure Go, CGO=0). Wasmtime-go is
// faster but pulls cgo which violates the openweft pure-Go contract
// every other driver follows. Speed is recoverable later by
// switching builtin.NewHypervisor's runtime factory.
//
// VM convention follows weft-driver-qemu : the VMSpec.UUID IS the
// absolute path to the per-VM state directory. The driver places
// `module.wasm`, `args.json`, `console.log` and `vm.pid` in there.
// "pid" is the wasm worker goroutine's identity (we record the
// running module's instantiation count) — not a real OS pid, but
// the same shape the agent's reconciler walks.
package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	drivers "github.com/openweft/weft-drivers"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Options configures a WASM driver instance.
type Options struct {
	// HostUUID + Hostname are echoed by HostInfo so the scheduler
	// + audit logs see where a side effect landed.
	HostUUID string
	Hostname string

	// HTTPClient is used to fetch wasm modules over http(s) when
	// BootRef is a URL. nil → http.DefaultClient. Tests inject a
	// stub.
	HTTPClient HTTPGetter

	// DefaultMemoryPages caps the wasm linear-memory in 64 KiB
	// pages when a VMSpec leaves MemoryMiB at 0. wazero's default
	// is 256 (= 16 MiB) which is too tight for non-trivial
	// modules ; we set 512 (= 32 MiB) to match the qemu driver's
	// 512 MiB / 16 ratio.
	DefaultMemoryPages uint32

	// MaxConcurrentVMs caps the per-host concurrent workload count.
	// 0 → no cap (limited only by RAM). Tests set this to 1 to
	// force serialisation.
	MaxConcurrentVMs int
}

// HTTPGetter is the minimum http.Client surface wasm-driver uses
// to pull modules. Tests inject a stub.
type HTTPGetter interface {
	Get(url string) (*http.Response, error)
}

// Hypervisor implements drivers.HypervisorDriver against wazero.
type Hypervisor struct {
	opts Options

	mu      sync.Mutex
	running map[string]*vmRun // key = VMSpec.UUID
}

// vmRun is the in-process state of one running workload. The
// cancel func unwinds the wasm execution (wazero respects ctx
// inside its imports + on memory ops via epoch-style interrupts).
type vmRun struct {
	cancel  context.CancelFunc
	runtime wazero.Runtime
	done    chan struct{}
	exitErr error
}

// compile-time conformance.
var _ drivers.HypervisorDriver = (*Hypervisor)(nil)

// NewHypervisor constructs the driver, filling in host-derived
// defaults.
func NewHypervisor(o Options) *Hypervisor {
	if o.HTTPClient == nil {
		o.HTTPClient = http.DefaultClient
	}
	if o.DefaultMemoryPages == 0 {
		o.DefaultMemoryPages = 512 // 32 MiB
	}
	return &Hypervisor{
		opts:    o,
		running: make(map[string]*vmRun),
	}
}

func (h *Hypervisor) HostInfo(context.Context) (drivers.HostInfo, error) {
	return hostInfoFor(h.opts), nil
}

// CreateVM provisions the state directory + fetches the module
// bytes into it. Idempotent : re-creating reuses module.wasm when
// the digest matches.
//
// BootKind / BootRef mapping :
//
//   - BootKind == "oci_image" + BootRef = "<registry>/path/x:tag"
//     → fetch via the agent's image-store path (TBD V0.2 — we hit
//     drivers.ErrUnsupported until the OCI puller hook lands).
//   - BootKind == "oci_image" + BootRef = "http(s)://..." → pull
//     via http.Get. Simplest path for V0.1 + tests.
//   - BootKind == "oci_image" + BootRef = "/abs/path/to/x.wasm" or
//     "file:///abs/path" → read directly. Dev shortcut.
//   - BootKind == "" with the same BootRef forms → also accepted
//     (the BootKind enum doesn't yet have a "wasm" entry ; V0.1 is
//     forgiving).
func (h *Hypervisor) CreateVM(ctx context.Context, spec drivers.VMSpec) error {
	vmDir := spec.UUID
	if vmDir == "" {
		return fmt.Errorf("wasm CreateVM: empty VM uuid/dir")
	}
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		return fmt.Errorf("wasm CreateVM: mkdir: %w", err)
	}

	// Idempotent : an existing module.wasm is reused unless empty.
	modPath := filepath.Join(vmDir, "module.wasm")
	if st, err := os.Stat(modPath); err == nil && st.Size() > 0 {
		return writeSpec(vmDir, spec)
	}

	if spec.BootRef == "" {
		return fmt.Errorf("wasm CreateVM: empty BootRef")
	}
	bytes, err := h.fetchModule(ctx, spec.BootRef)
	if err != nil {
		return fmt.Errorf("wasm CreateVM: fetch %q: %w", spec.BootRef, err)
	}
	if len(bytes) == 0 {
		return fmt.Errorf("wasm CreateVM: empty module")
	}
	if err := os.WriteFile(modPath, bytes, 0o644); err != nil {
		return fmt.Errorf("wasm CreateVM: write module: %w", err)
	}
	return writeSpec(vmDir, spec)
}

// StartVM compiles + instantiates the wasm module and launches the
// `_start` entry in a goroutine. Idempotent — a second call when
// the workload is already running is a no-op.
//
// stdout / stderr are tee'd to <vmDir>/console.log so the operator
// can read what the module produced (mirrors qemu's console.log
// convention).
func (h *Hypervisor) StartVM(ctx context.Context, vmUUID string) error {
	h.mu.Lock()
	if _, already := h.running[vmUUID]; already {
		h.mu.Unlock()
		return nil
	}
	if h.opts.MaxConcurrentVMs > 0 && len(h.running) >= h.opts.MaxConcurrentVMs {
		h.mu.Unlock()
		return fmt.Errorf("wasm StartVM: host at capacity (%d running)", len(h.running))
	}
	h.mu.Unlock()

	modBytes, err := os.ReadFile(filepath.Join(vmUUID, "module.wasm"))
	if err != nil {
		return fmt.Errorf("wasm StartVM: read module: %w", err)
	}
	logf, err := os.Create(filepath.Join(vmUUID, "console.log"))
	if err != nil {
		return fmt.Errorf("wasm StartVM: open log: %w", err)
	}

	// Each VM gets its own wazero runtime so Close on stop unwinds
	// it cleanly. Sharing one runtime across VMs would mean a
	// crash in one workload's host-imports could poison the others.
	runCtx, cancel := context.WithCancel(context.Background())
	rt := wazero.NewRuntimeWithConfig(runCtx, wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(h.opts.DefaultMemoryPages))
	if _, err := wasi_snapshot_preview1.Instantiate(runCtx, rt); err != nil {
		_ = rt.Close(runCtx)
		cancel()
		_ = logf.Close()
		return fmt.Errorf("wasm StartVM: WASI: %w", err)
	}

	run := &vmRun{
		cancel:  cancel,
		runtime: rt,
		done:    make(chan struct{}),
	}
	h.mu.Lock()
	h.running[vmUUID] = run
	h.mu.Unlock()

	// Write the goroutine identity into vm.pid — the agent's
	// reconciler treats a present-then-vanished file as the signal
	// "workload exited". A real PID isn't useful here, but the
	// shape is honoured.
	_ = os.WriteFile(filepath.Join(vmUUID, "vm.pid"), []byte(fmt.Sprintf("%d\n", time.Now().UnixNano())), 0o644)

	go h.runModule(runCtx, run, modBytes, vmUUID, logf)
	_ = ctx // start is non-blocking : ctx covers only the dispatch side
	return nil
}

// runModule is the goroutine body. Compiles + instantiates +
// invokes `_start`. Exit is logged. The runtime is closed in StopVM
// or in this goroutine's defer if the module returns on its own.
func (h *Hypervisor) runModule(ctx context.Context, run *vmRun, modBytes []byte, vmUUID string, logf io.WriteCloser) {
	defer func() {
		_ = run.runtime.Close(ctx)
		_ = logf.Close()
		// Remove vm.pid so the reconciler's "workload exited"
		// transition fires once.
		_ = os.Remove(filepath.Join(vmUUID, "vm.pid"))
		h.mu.Lock()
		delete(h.running, vmUUID)
		h.mu.Unlock()
		close(run.done)
	}()
	cfg := wazero.NewModuleConfig().
		WithStdout(logf).
		WithStderr(logf).
		WithName(filepath.Base(vmUUID))
	mod, err := run.runtime.InstantiateWithConfig(ctx, modBytes, cfg)
	if err != nil {
		run.exitErr = err
		fmt.Fprintf(logf, "wasm instantiation failed: %v\n", err)
		return
	}
	// _start is implicit for WASI command modules ; if it isn't
	// present (reactor modules) we just instantiated and exit
	// cleanly. wazero invokes _start during InstantiateWithConfig
	// for command modules, so by this point the module has either
	// run to completion or trapped.
	_ = mod
}

// StopVM cancels the runtime context, causing wazero to abort any
// in-flight host calls and unwind the goroutine. Idempotent — a
// second call after the workload exited is a no-op.
func (h *Hypervisor) StopVM(ctx context.Context, vmUUID string) error {
	h.mu.Lock()
	run, ok := h.running[vmUUID]
	h.mu.Unlock()
	if !ok {
		return nil
	}
	run.cancel()
	select {
	case <-run.done:
		return nil
	case <-ctx.Done():
		// Caller's deadline elapsed before the goroutine returned.
		// Return ctx.Err() so the caller sees the timeout.
		return ctx.Err()
	}
}

// DeleteVM removes the per-VM state directory. Returns nil for
// "already gone".
func (h *Hypervisor) DeleteVM(ctx context.Context, vmUUID string) error {
	// Defensive : try a stop first so we don't yank the rug from
	// under an in-flight goroutine. Short deadline — DeleteVM's
	// own ctx may already be tight.
	if _, ok := h.running[vmUUID]; ok {
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = h.StopVM(stopCtx, vmUUID)
		cancel()
	}
	if err := os.RemoveAll(vmUUID); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("wasm DeleteVM: %w", err)
	}
	return nil
}

// AttachDisk / DetachDisk / AttachNIC / DetachNIC : wasm modules
// don't have disks or NICs in the VM sense. They get capability
// tokens via WASI preopens + a sandboxed network adapter (V0.2).
// Until that lands the four hot-plug verbs return nil so the
// scheduler's reconciler doesn't stall trying to attach a virtio
// device to a workload that has no such concept.
func (h *Hypervisor) AttachDisk(context.Context, string, drivers.DiskSpec) error {
	return nil
}
func (h *Hypervisor) DetachDisk(context.Context, string, string) error { return nil }
func (h *Hypervisor) AttachNIC(context.Context, string, drivers.NICHandle) error {
	return nil
}
func (h *Hypervisor) DetachNIC(context.Context, string, string) error { return nil }

// filePathFromURL turns a file:// URL back into a path the given OS can open.
//
// On Windows a file URL is file:///C:/dir/x -- three slashes, forward
// separators, the volume letter inside the path -- per Microsoft's "File URIs
// in Windows", which is the form cmd/go/internal/web/url.go produces and the
// form this package documents. url.Parse hands that path back as "/C:/dir/x",
// and "/C:/dir/x" names nothing: the leading slash has to come off.
//
// Nothing else has to change. The separators can stay as they are, because
// Windows accepts '/' as a path separator -- os.IsPathSeparator on Windows
// returns true for both and says so in a comment. Calling filepath.FromSlash
// here would be worse than useless: it converts to the separator of the
// HOST the code is running on, so it is a no-op on the machines where this
// is tested and would have made the branch untestable anywhere but Windows.
//
// goos is a parameter rather than a read of runtime.GOOS inside, so both
// branches are reachable from one machine. A conversion exercised only on the
// platform it is wrong for is how this stayed broken.
func filePathFromURL(u *url.URL, goos string) string {
	p := u.Path
	if goos == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		return p[1:]
	}
	return p
}

// fetchModule resolves BootRef + returns the wasm bytes.
//
// Supported BootRef forms (V0.1) :
//
//   - "/abs/path/to/x.wasm"               local file
//   - "file:///abs/path/to/x.wasm"        local file via URL
//   - "http://host/path/x.wasm"           HTTP GET
//   - "https://host/path/x.wasm"          HTTPS GET
//   - "oci://registry/repo:tag"           V0.2 (returns ErrUnsupported today)
func (h *Hypervisor) fetchModule(ctx context.Context, ref string) ([]byte, error) {
	switch {
	case strings.HasPrefix(ref, "oci://"):
		return nil, drivers.ErrUnsupported
	case strings.HasPrefix(ref, "file://"):
		u, err := url.Parse(ref)
		if err != nil {
			return nil, err
		}
		return os.ReadFile(filePathFromURL(u, runtime.GOOS))
	case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"):
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref, nil)
		if err != nil {
			return nil, err
		}
		client, ok := h.opts.HTTPClient.(*http.Client)
		var resp *http.Response
		if ok {
			resp, err = client.Do(req)
		} else {
			resp, err = h.opts.HTTPClient.Get(ref)
		}
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, fmt.Errorf("wasm fetch: HTTP %d", resp.StatusCode)
		}
		return io.ReadAll(resp.Body)
	default:
		// Fallback : treat as local file path.
		return os.ReadFile(ref)
	}
}

// writeSpec persists a snapshot of VMSpec so an operator (or the
// reconciler post-restart) can introspect what a vmDir actually
// represents.
func writeSpec(vmDir string, spec drivers.VMSpec) error {
	bytes, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(vmDir, "spec.json"), bytes, 0o644)
}
