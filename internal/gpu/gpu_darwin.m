//go:build darwin && !nogpu

#import <Metal/Metal.h>
#import <Foundation/Foundation.h>
#import <IOKit/IOKitLib.h>

typedef struct {
    const char* name;
    unsigned long long allocated_size;
    unsigned long long recommended_max;
    int has_unified_memory;
} GPUInfo;

typedef struct {
    int thermal_state;
} ThermalInfo;

GPUInfo getMetalGPUInfo() {
    GPUInfo info = {0};

    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (device == nil) {
        return info;
    }

    NSString *name = [device name];
    if (name != nil) {
        info.name = strdup([name UTF8String]);
    }

    info.allocated_size = [device currentAllocatedSize];
    info.recommended_max = [device recommendedMaxWorkingSetSize];
    info.has_unified_memory = [device hasUnifiedMemory] ? 1 : 0;

    return info;
}

ThermalInfo getThermalState() {
    ThermalInfo info = {0};

    // Use NSProcessInfo thermalState as a proxy
    NSProcessInfoThermalState state = [[NSProcessInfo processInfo] thermalState];
    info.thermal_state = (int)state;

    return info;
}
