//go:build darwin && cgo

package daemon

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <IOKit/IOKitLib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <math.h>
static int perf_gpu(double *device,double *renderer,double *tiler,unsigned long long *memory){
 io_iterator_t iterator=0;if(IOServiceGetMatchingServices(kIOMainPortDefault,IOServiceMatching("AGXAccelerator"),&iterator)!=KERN_SUCCESS)return 1;
 io_service_t service=IOIteratorNext(iterator);io_service_t extra=IOIteratorNext(iterator);IOObjectRelease(iterator);
 if(!service || extra){if(service)IOObjectRelease(service);if(extra)IOObjectRelease(extra);return 1;}
 CFTypeRef raw=IORegistryEntryCreateCFProperty(service,CFSTR("PerformanceStatistics"),kCFAllocatorDefault,0);IOObjectRelease(service);
 if(!raw || CFGetTypeID(raw)!=CFDictionaryGetTypeID()){if(raw)CFRelease(raw);return 1;}
 CFStringRef keys[]={CFSTR("Device Utilization %"),CFSTR("Renderer Utilization %"),CFSTR("Tiler Utilization %"),CFSTR("In use system memory")};
 double values[4];int invalid=0;
 for(int i=0;i<4;i++){CFTypeRef n=CFDictionaryGetValue((CFDictionaryRef)raw,keys[i]);if(!n || CFGetTypeID(n)!=CFNumberGetTypeID() || !CFNumberGetValue((CFNumberRef)n,kCFNumberDoubleType,&values[i]) || !isfinite(values[i]) || values[i]<0)invalid=1;}
 CFRelease(raw);if(invalid || values[0]>100 || values[1]>100 || values[2]>100)return 1;
 *device=values[0];*renderer=values[1];*tiler=values[2];*memory=(unsigned long long)values[3];return 0;
}
*/
import "C"

func performanceGPU() (performanceGPUSample, error) {
	var a, b, c C.double
	var memory C.ulonglong
	if C.perf_gpu(&a, &b, &c, &memory) != 0 {
		return performanceGPUSample{}, errPerformanceMetricUnavailable
	}
	return performanceGPUSample{Device: float64(a), Renderer: float64(b), Tiler: float64(c), Memory: uint64(memory)}, nil
}
