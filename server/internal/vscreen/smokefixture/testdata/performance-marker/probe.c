#include "performance_marker.h"
#include <stdio.h>
#include <string.h>
int main(void){uint8_t bytes[20];multica_performance_marker(bytes,0x01020304,42,123456789);char encoded[41]={0};for(int i=0;i<20;i++)sprintf(encoded+i*2,"%02x",bytes[i]);puts(encoded);return strcmp(encoded,"5653010203040000002a00000000075bcd15e315")!=0;}
