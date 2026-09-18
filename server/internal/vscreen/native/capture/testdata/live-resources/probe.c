#include "live_resources.h"
#include <pthread.h>
#include <stdio.h>
#include <string.h>
uint32_t vs_capture_live_count(void){return 0;}
static void *work(void *unused){for(int i=0;i<10000;i++){if(!vs_live_enter(VS_LIVE_CALLBACK))return (void *)1;vs_live_exit(VS_LIVE_CALLBACK);}return NULL;}
int main(int argc,char **argv){
 if(argc>1 && !strcmp(argv[1],"underflow")){vs_live_exit(VS_LIVE_ENCODER);VSLiveResources s=vs_live_resources();printf("{\"underflow_rejected\":%s,\"encoder_count\":%u}\n",!s.valid?"true":"false",s.encoder_sessions);return s.valid||s.encoder_sessions?1:0;}
 pthread_t workers[4];for(int i=0;i<4;i++)if(pthread_create(&workers[i],NULL,work,NULL))return 1;
 for(int i=0;i<4;i++){void *result;pthread_join(workers[i],&result);if(result)return 1;}
 if(!vs_live_enter(VS_LIVE_ENCODER))return 1;
 VSLiveResources retained=vs_live_resources();
 vs_live_exit(VS_LIVE_ENCODER);
 VSLiveResources closed=vs_live_resources();
 printf("{\"concurrent_callbacks_balanced\":%s,\"unconfirmed_retained_encoder\":%u,\"confirmed_release_encoder\":%u,\"valid\":%s}\n",closed.active_callbacks==0?"true":"false",retained.encoder_sessions,closed.encoder_sessions,closed.valid?"true":"false");
 return retained.encoder_sessions!=1||closed.encoder_sessions!=0||closed.active_callbacks!=0||!closed.valid;
}
