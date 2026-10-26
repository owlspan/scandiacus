#include <stdio.h>
#include <stdlib.h>
typedef struct { double x, y, z, vx, vy; } S;
int main(void) { S **ps = malloc(1000*sizeof(S*)); for (int i=0;i<1000;i++){S*p=malloc(sizeof(S));p->x=i;p->y=0.5;p->z=1.0;p->vx=0.5;p->vy=1.0;ps[i]=p;}
for (int s=0;s<20000;s++) for(int i=0;i<1000;i++){ps[i]->x += ps[i]->vx * 0.01;ps[i]->y += ps[i]->vy * 0.01;ps[i]->z += ps[i]->x * 0.0001;if(ps[i]->x>100.0)ps[i]->vx=-ps[i]->vx;}
double t=0; for(int i=0;i<1000;i++) t+=ps[i]->x+ps[i]->y+ps[i]->z; printf("%.17g\n",t); return 0; }
