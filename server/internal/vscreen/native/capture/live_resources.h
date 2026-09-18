#ifndef MULTICA_VSCREEN_LIVE_RESOURCES_H
#define MULTICA_VSCREEN_LIVE_RESOURCES_H
#include <stdbool.h>
#include <stdint.h>
enum { VS_LIVE_ENCODER=0, VS_LIVE_CALLBACK=1 };
typedef struct {uint32_t capture_sessions, encoder_sessions, active_callbacks; bool valid;} VSLiveResources;
bool vs_live_enter(unsigned kind);
void vs_live_exit(unsigned kind);
VSLiveResources vs_live_resources(void);
uint32_t vs_capture_live_count(void);
#endif
