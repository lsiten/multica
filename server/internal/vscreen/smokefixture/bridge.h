#include <stdint.h>
int multica_smoke_fixture_register(const char *path);
char *multica_smoke_fixture_start(int pid);
int multica_smoke_fixture_run(const char *config);
char *multica_smoke_fixture_foreground(void);
