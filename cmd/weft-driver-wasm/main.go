// Command weft-driver-wasm is the WASM hypervisor driver as an
// external weft go-plugin. Pure Go (it embeds wazero ; no cgo, no
// system binary), so it builds and runs on every platform openweft
// targets — amd64 / arm64 / riscv64 / loongarch64 alike.
//
// It has a single mode : launched by weft with no arguments, it
// serves the four driver services over go-plugin. Launch-time
// config arrives via env (set by the launching weft process).
package main

import (
	"log/slog"
	"os"
	"strconv"

	weftplugin "github.com/openweft/weft-driver-plugin"
	wasmdriver "github.com/openweft/weft-driver-wasm/builtin"
	weftslognats "github.com/openweft/weft-slognats"
)

// envInt reads an int env var, returning 0 when unset or malformed
// so the driver falls back to its built-in default.
func envInt(name string) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 0
}

// Optional WASM tuning passed through from the host. Empty values
// keep the driver's defaults.
const (
	envWasmDefaultMemPages = "WEFT_WASM_DEFAULT_MEM_PAGES"
	envWasmMaxConcurrent   = "WEFT_WASM_MAX_CONCURRENT_VMS"
)

func main() {
	hostUUID := os.Getenv(weftplugin.EnvHostUUID)
	log, logCloser := weftslognats.SetupFromEnv("weft.driver.wasm." + hostUUID + ".log")
	defer logCloser.Close()
	slog.SetDefault(log)
	// Capture Go panics via NATS slog fan-out so weft-doctor sees
	// driver-plugin crashes alongside agent crashes. Matches the
	// pattern weft v0.4.9 introduced for the main agent.
	defer weftslognats.PanicReporter("weft-driver-wasm")

	defPages := uint32(envInt(envWasmDefaultMemPages))
	maxConcurrent := envInt(envWasmMaxConcurrent)
	b := wasmdriver.New(wasmdriver.BundleOptions{
		Options: wasmdriver.Options{
			HostUUID:           hostUUID,
			Hostname:           os.Getenv(weftplugin.EnvHostname),
			DefaultMemoryPages: defPages,
			MaxConcurrentVMs:   maxConcurrent,
		},
	})
	weftplugin.Serve(weftplugin.DriverSet{
		Hypervisor: b.Hypervisor,
		Network:    b.Network,
		Volume:     b.Volume,
		Image:      b.Image,
	})
}
