//go:build darwin

package gpu

/*
#cgo LDFLAGS: -framework Metal -framework Foundation -framework IOKit
#include <stdlib.h>

// Metal GPU info
typedef struct {
    const char* name;
    unsigned long long allocated_size;
    unsigned long long recommended_max;
    int has_unified_memory;
} GPUInfo;

// IOKit thermal state
typedef struct {
    int thermal_state; // 0=nominal, 1=fair, 2=serious, 3=critical
} ThermalInfo;

extern GPUInfo getMetalGPUInfo();
extern ThermalInfo getThermalState();
*/
import "C"
import "unsafe"

func queryGPU() Info {
	gpuInfo := C.getMetalGPUInfo()
	thermalInfo := C.getThermalState()

	name := ""
	if gpuInfo.name != nil {
		name = C.GoString(gpuInfo.name)
		C.free(unsafe.Pointer(gpuInfo.name))
	}

	allocated := uint64(gpuInfo.allocated_size)
	recommended := uint64(gpuInfo.recommended_max)

	var usagePercent float64
	if recommended > 0 {
		usagePercent = float64(allocated) / float64(recommended) * 100
	}

	thermalState := "nominal"
	switch thermalInfo.thermal_state {
	case 1:
		thermalState = "fair"
	case 2:
		thermalState = "serious"
	case 3:
		thermalState = "critical"
	}

	return Info{
		Name:             name,
		AllocatedBytes:   allocated,
		RecommendedMax:   recommended,
		UsagePercent:     usagePercent,
		ThermalState:     thermalState,
		HasUnifiedMemory: gpuInfo.has_unified_memory != 0,
	}
}
