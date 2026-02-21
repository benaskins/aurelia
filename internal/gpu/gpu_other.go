//go:build !darwin || nogpu

package gpu

// queryGPU returns an empty Info on non-darwin platforms.
// GPU observability via Metal/IOKit is only available on macOS.
func queryGPU() Info {
	return Info{}
}
