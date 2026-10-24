#include <stdio.h>
typedef struct { double x, y, vx, vy; } P;
int main(void) { static P ps[1000]; for (int i=0;i<1000;i++){ps[i].x=i;ps[i].y=0;ps[i].vx=1;ps[i].vy=0.5;}
for (int s=0;s<20000;s++) for(int i=0;i<1000;i++){ps[i].x+=ps[i].vx*0.01;ps[i].y+=ps[i].vy*0.01;if(ps[i].x>100.0)ps[i].vx=-ps[i].vx;}
double t=0; for(int i=0;i<1000;i++) t+=ps[i].x+ps[i].y; printf("%.17g\n",t); return 0; }
