//go:build darwin && cgo

#include "live_resources.h"
#include <stdatomic.h>
#include <limits.h>
static atomic_uint counts[2];
static atomic_bool invalid;
bool vs_live_enter(unsigned kind) {
 if(kind>=2){atomic_store(&invalid,true);return false;}
 unsigned value=atomic_load(&counts[kind]);
 while(value!=UINT_MAX){if(atomic_compare_exchange_weak(&counts[kind],&value,value+1))return true;}
 atomic_store(&invalid,true);return false;
}
void vs_live_exit(unsigned kind) {
 if(kind>=2){atomic_store(&invalid,true);return;}
 unsigned value=atomic_load(&counts[kind]);
 while(value){if(atomic_compare_exchange_weak(&counts[kind],&value,value-1))return;}
 atomic_store(&invalid,true);
}
VSLiveResources vs_live_resources(void) {
 return (VSLiveResources){vs_capture_live_count(),atomic_load(&counts[VS_LIVE_ENCODER]),atomic_load(&counts[VS_LIVE_CALLBACK]),!atomic_load(&invalid)};
}
