#import "native_internal.h"
Boolean CompletionProbeAccess(void);
void CompletionProbePost(pid_t,CGEventRef);
#define CGPreflightPostEventAccess CompletionProbeAccess
#define CGEventPostToPid CompletionProbePost
