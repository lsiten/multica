#include <stdint.h>
#include <stdlib.h>
uintptr_t ac_new(void);
void ac_free(uintptr_t);
uintptr_t ac_request_new(double);
void ac_request_cancel(uintptr_t);
void ac_request_free(uintptr_t);
int ac_call(uintptr_t, uintptr_t, const char *, size_t, char **, size_t *);
