# weft-driver-wasm

WebAssembly hypervisor driver for [openweft](https://github.com/openweft).

Turns any host running `weft-agent` into a hypervisor for WASI workloads — no KVM, no Apple VZ, no nested virtualisation required. Pure Go, embeds [wazero](https://wazero.io/), runs on every architecture openweft targets (amd64 / arm64 / riscv64 / loongarch64).

## Why

The classic openweft drivers (`weft-driver-vz`, `weft-driver-qemu`, `weft-driver-vmd`, `weft-driver-dcs`) each need a real hypervisor on the host. This excludes :

- Browsers (no KVM, no IOMMU).
- Constrained boxes (no virt extensions, no qemu install).
- Mixed-arch developer fleets where building a full guest kernel for every arch is overkill for ephemeral workloads.

`weft-driver-wasm` makes those hosts useful : the user's PC becomes its own hypervisor, scheduling WASM modules from OCI artifacts. The same scheduler / planner / catalogue path that runs VMs runs wasm workloads — they're just a fourth workload kind alongside microVM and classic VM.

## How

```
┌─────────────────────────┐  go-plugin (gRPC)  ┌──────────────────────┐
│ weft-agent              │ <────────────────> │ weft-driver-wasm     │
│   - Adapter             │                    │   wazero runtime     │
│   - Scheduler           │                    │   per-VM goroutine   │
│   - Reconciler          │                    │   stdout → console.log│
└─────────────────────────┘                    └──────────────────────┘
```

Lifecycle (mirrors `drivers.HypervisorDriver`):

| RPC | What it does |
|-----|---|
| `CreateVM` | Make `<vmDir>/`, fetch wasm module bytes into `module.wasm`. |
| `StartVM` | Compile + instantiate the module. Spawn the `_start` goroutine. Tee stdout/stderr to `console.log`. |
| `StopVM`  | Cancel the runtime context. wazero unwinds in-flight host calls. |
| `DeleteVM`| Remove `<vmDir>/`. Stop first if still running. |
| `Attach*` / `Detach*` | No-ops in V0.1. Wasm has no disks / NICs in the VM sense — V0.2 will surface WASI preopens + capability tokens through the same shape. |

## VMSpec mapping

`VMSpec.BootRef` accepts :

- `/abs/path/to/x.wasm` — local file (dev shortcut).
- `file:///abs/path/to/x.wasm` — local file via URL.
- `http(s)://host/.../x.wasm` — fetched once on `CreateVM`.
- `oci://<registry>/<repo>:<tag>` — V0.2 (not yet wired).

V0.1 leaves `BootKind` permissive (any value works) ; a `wasm` enumerator on `BootKind` lands in `weft-proto v0.13.0`.

## Build

```sh
go build -o weft-driver-wasm ./cmd/weft-driver-wasm
```

Cross-compile freely — wazero is pure Go, this driver inherits CGO=0. The release matrix : darwin/amd64, darwin/arm64, linux/amd64, linux/arm64, linux/riscv64, linux/loong64.

## Configuration

Env vars consumed by `weft-driver-wasm` :

| Var | Effect |
|-----|---|
| `WEFT_WASM_DEFAULT_MEM_PAGES` | Wasmtime linear-memory cap in 64 KiB pages. Default 512 (= 32 MiB). |
| `WEFT_WASM_MAX_CONCURRENT_VMS` | Per-host concurrent workload cap. 0 = unlimited. |

The standard plugin vars (`WEFT_HOST_UUID`, `WEFT_HOSTNAME`, `WEFT_STATE_DIR`) come from `weft-driver-plugin`.

## What's coming

V0.2 work, documented in `weft/docs/wasm-driver-design.md` :

- OCI artifact pull via oras-go (mediatype `application/wasm`).
- WASI preopen capabilities (filesystem subset) wired through `AttachDisk`.
- Agent-proxied outbound L3 surfaced as `wasi:http`.
- Browser-side worker pool (Go/wasm-js agent dials `weft-agent` over WebSocket ; this same driver dispatches).
- `BootKind = "wasm"` enumerator + matching protobuf in `weft-proto`.

## Licence

BSD 3-Clause, copyright "openweft contributors". See `LICENSE`.
