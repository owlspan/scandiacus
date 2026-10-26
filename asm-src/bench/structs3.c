#include <stdio.h>
#include <stdlib.h>
typedef struct { double x, y, v; } S;
static S ps[1000];
int main(void) { for (int i=0;i<1000;i++){S*p=&ps[i];p->x=i;p->y=0.5;p->v=1.0;}
for (int s=0;s<20000;s++) for(int i=0;i<1000;i++){ps[i].x += ps[i].v * 0.01;ps[i].y += ps[i].v * 0.02;if(ps[i].x>100.0)ps[i].v=-ps[i].v;}
double t=0; for(int i=0;i<1000;i++) t+=ps[i].x+ps[i].y+ps[i].v; printf("%.17g\n",t); return 0; }
