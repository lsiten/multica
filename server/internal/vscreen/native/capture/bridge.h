#include <stddef.h>
#include <stdint.h>
typedef struct {
  uint32_t display_id, width, height, fps, bitrate, max_level;
  const uint32_t *excluded;
  uint32_t excluded_count;
  int cursor;
} VSCaptureConfig;
typedef struct {
  uint8_t *bytes;
  uint32_t length;
  int64_t pts_ns, duration_ns;
  int keyframe;
} VSEncodedSample;
int vs_capture_open(VSCaptureConfig config, uintptr_t *handle);
int vs_capture_next(uintptr_t handle, uint32_t timeout_ms,
                    VSEncodedSample *sample);
int vs_capture_force_keyframe(uintptr_t handle);
int vs_capture_close(uintptr_t handle, uint32_t timeout_ms);
void vs_sample_free(VSEncodedSample *sample);
int vs_encoder_fixture(VSCaptureConfig config, uintptr_t *handle);
int vs_encoder_fixture_frame(uintptr_t handle, uint32_t index);

typedef struct {
  uint32_t retained_input, in_flight, queued_samples, peak_in_flight,
      peak_queued;
  uint64_t dropped_input, dropped_output;
  int hardware, hardware_known, closed;
  char profile_level[7];
} VSStreamStats;
int vs_capture_stats(uintptr_t handle, VSStreamStats *stats);
int vs_capture_permission(void);
