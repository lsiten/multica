#ifndef MULTICA_PERFORMANCE_MARKER_H
#define MULTICA_PERFORMANCE_MARKER_H
#include <stdint.h>
static inline void multica_performance_marker(uint8_t bytes[20],uint32_t source,uint32_t frame,uint64_t stamp) {
 bytes[0]=0x56;bytes[1]=0x53;
 for(int i=0;i<4;i++){bytes[2+i]=(source>>(24-8*i))&255;bytes[6+i]=(frame>>(24-8*i))&255;}
 for(int i=0;i<8;i++)bytes[10+i]=(stamp>>(56-8*i))&255;
 uint16_t crc=0xffff;
 for(int i=0;i<18;i++){crc^=(uint16_t)bytes[i]<<8;for(int bit=0;bit<8;bit++)crc=(crc&0x8000)?(crc<<1)^0x1021:crc<<1;}
 bytes[18]=crc>>8;bytes[19]=crc&255;
}
#endif
