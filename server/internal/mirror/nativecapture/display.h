#include <stdint.h>
typedef struct {
  uint32_t id, width, height;
  int32_t x, y;
  int main, builtin;
  double logical_width, logical_height, scale;
  char name[256];
  char uuid[128];
} MMRDisplay;
int mmr_supported(void);
int mmr_list(MMRDisplay *out, uint32_t capacity, uint32_t *count);
void mmr_run(void);
void mmr_stop(void);
