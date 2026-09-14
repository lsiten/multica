#include <stdint.h>
typedef struct {
  uint32_t id, width, height;
  int32_t x, y;
  int recording, capture, main;
  uint32_t mirror;
  int builtin, managed;
  double logical_width, logical_height, scale;
  char name[256];
  char uuid[128];
} VSDisplay;
int vs_supported(void);
int vs_create(const char *name, uint32_t serial, uint32_t width,
              uint32_t height, VSDisplay *out);
int vs_describe(uint32_t id, VSDisplay *out);
int vs_list(VSDisplay *out, uint32_t capacity, uint32_t *count);
int vs_dispose(uint32_t id);
void vs_run(void);
void vs_stop(void);
