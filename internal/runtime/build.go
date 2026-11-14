// Package runtime installs and runs the local model engine (llama.cpp's
// llama-server) so SafeShell needs nothing installed by hand. Downloads are
// pinned by SHA-256, resumable, and verified before use.
package runtime

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/samarthsinh2660/safeshell/internal/xdg"
)

// Tag is the pinned llama.cpp release.
const Tag = "b11429"

// Asset is one archive in a release.
type Asset struct {
	Name string
	Size int64
	SHA  string
}

func (a Asset) URL() string {
	return "https://github.com/ggml-org/llama.cpp/releases/download/" + Tag + "/" + a.Name
}

// Build is one prebuilt engine: the llama.cpp archive plus any runtime
// libraries it needs (the CUDA builds need NVIDIA's runtime libraries).
type Build struct {
	Variant string // cuda13, cuda12, vulkan or cpu
	Main    Asset
	Extra   []Asset
}

// Size is the total download.
func (b Build) Size() int64 {
	n := b.Main.Size
	for _, e := range b.Extra {
		n += e.Size
	}
	return n
}

// GPU reports whether the build uses a graphics card.
func (b Build) GPU() bool { return b.Variant != "cpu" }

func a(name string, size int64, sha string) Asset { return Asset{Name: name, Size: size, SHA: sha} }

var builds = map[string]Build{
	"amd64/cpu":    {Variant: "cpu", Main: a("llama-"+Tag+"-bin-ubuntu-x64.tar.gz", 17693462, "f6d25dde8f51133143d1453da4fd5f73b145127177612a283bf7995957af3392")},
	"amd64/vulkan": {Variant: "vulkan", Main: a("llama-"+Tag+"-bin-ubuntu-vulkan-x64.tar.gz", 31636673, "632c4e98feba2b94407a2130e3133e0c3aefb0ea1ab41337e926d8bfafdd0b74")},
	"arm64/cpu":    {Variant: "cpu", Main: a("llama-"+Tag+"-bin-ubuntu-arm64.tar.gz", 13682895, "ed44c0f79c3d02424f62bdc87e106318b0b89f49050e2e80c5f352723d14e00b")},
	"arm64/vulkan": {Variant: "vulkan", Main: a("llama-"+Tag+"-bin-ubuntu-vulkan-arm64.tar.gz", 24845090, "702d99c4219b4314cc6d3b10fb41ae96cbfc68226305c2681269dfb51487af34")},
	"amd64/cuda13": {Variant: "cuda13",
		Main:  a("llama-"+Tag+"-bin-ubuntu-cuda-13.4-x64.tar.gz", 152519318, "8082b7eaa74a714c9fecca19128f751c8e32da763ee8096b8ad1e824da7621d3"),
		Extra: []Asset{a("cudart-llama-"+Tag+"-bin-ubuntu-cuda-13.4-x64.tar.gz", 440236630, "93d18648d815b2bd624d83d82f653e1db97afb478f02064305fe3cf570040a6d")}},
	"amd64/cuda12": {Variant: "cuda12",
		Main:  a("llama-"+Tag+"-bin-ubuntu-cuda-12.8-x64.tar.gz", 171651873, "b13c64b9224d3c89d9993945d70cc21177ad00178c21b8d34237e3bc139834f1"),
		Extra: []Asset{a("cudart-llama-"+Tag+"-bin-ubuntu-cuda-12.8-x64.tar.gz", 594377569, "f4ab593e31507320b121f2cb21791f4c72420a2483d3df8bf2ca1a97d04e6845")}},
}

// minDriver is the oldest NVIDIA driver (major version) each CUDA build supports.
var minDriver = map[string]int{"cuda13": 580, "cuda12": 570}

var vulkanLibs = []string{
	"/usr/lib/x86_64-linux-gnu/libvulkan.so.1", "/usr/lib/aarch64-linux-gnu/libvulkan.so.1",
	"/usr/lib64/libvulkan.so.1", "/usr/lib/libvulkan.so.1",
}

// exists and readFile are replaced in tests.
var (
	exists   = func(p string) bool { _, err := os.Stat(p); return err == nil }
	readFile = func(p string) string { b, _ := os.ReadFile(p); return string(b) }
)

// HasVulkan reports whether the machine has a Vulkan loader.
func HasVulkan() bool {
	for _, p := range vulkanLibs {
		if exists(p) {
			return true
		}
	}
	return false
}

var driverRe = regexp.MustCompile(`Module(?: for \S+)?\s+(\d+)\.\d+`)

// ParseNvidiaDriver returns the major version in /proc/driver/nvidia/version text, or 0.
func ParseNvidiaDriver(text string) int {
	m := driverRe.FindStringSubmatch(text)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// NvidiaDriver is the installed NVIDIA driver's major version, or 0 if there is none.
func NvidiaDriver() int { return ParseNvidiaDriver(readFile("/proc/driver/nvidia/version")) }

// Candidates lists the engines to try, best first; the CPU build is always
// last, so a GPU problem never leaves the person without AI. gpu is one of
// auto, cuda, vulkan or cpu ("on" and "off" from older settings still work).
func Candidates(goarch, gpu string) []Build {
	var out []Build
	add := func(variant string) {
		if b, ok := builds[goarch+"/"+variant]; ok {
			out = append(out, b)
		}
	}
	want := func(kind string) bool { return gpu == "auto" || gpu == "" || gpu == kind }
	if gpu == "off" {
		gpu = "cpu"
	}
	if gpu == "on" {
		gpu = "vulkan"
	}
	if want("cuda") {
		drv := NvidiaDriver()
		for _, v := range []string{"cuda13", "cuda12"} {
			if drv >= minDriver[v] {
				add(v)
				break
			}
		}
	}
	if want("vulkan") && (gpu == "vulkan" || HasVulkan()) {
		add("vulkan")
	}
	add("cpu")
	return out
}

// Dirs says where things live.
type Dirs struct{ Data, State string }

// DefaultDirs follows the XDG spec.
func DefaultDirs() Dirs {
	return Dirs{Data: xdg.Dir("XDG_DATA_HOME", ".local", "share"), State: xdg.Dir("XDG_STATE_HOME", ".local", "state")}
}

// EngineDir is where one build is unpacked.
func (d Dirs) EngineDir(b Build) string { return filepath.Join(d.Data, "engine", Tag+"-"+b.Variant) }

// ServerBin is the llama-server executable of a build.
func (d Dirs) ServerBin(b Build) string { return filepath.Join(d.EngineDir(b), "llama-server") }

// ModelPath is where a catalog file is stored.
func (d Dirs) ModelPath(file string) string { return filepath.Join(d.Data, "models", file) }
