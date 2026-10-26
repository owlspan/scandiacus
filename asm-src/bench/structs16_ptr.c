#include <stdio.h>
#include <stdlib.h>
typedef struct { double f0, f1, f2, f3, f4, f5, f6, f7, f8, f9, f10, f11, f12, f13, f14, f15; } S;
int main(void) { S **ps = malloc(1000*sizeof(S*)); for (int i=0;i<1000;i++){S*p=malloc(sizeof(S));p->f0=i;p->f1=0.5;p->f2=1.0;p->f3=0.5;p->f4=1.0;p->f5=0.5;p->f6=1.0;p->f7=0.5;p->f8=1.0;p->f9=0.5;p->f10=1.0;p->f11=0.5;p->f12=1.0;p->f13=0.5;p->f14=1.0;p->f15=0.5;ps[i]=p;}
for (int s=0;s<20000;s++) for(int i=0;i<1000;i++){ps[i]->f0 += ps[i]->f8 * 0.01;ps[i]->f1 += ps[i]->f9 * 0.01;ps[i]->f2 += ps[i]->f10 * 0.01;if(ps[i]->f0>100.0)ps[i]->f8=-ps[i]->f8;}
double t=0; for(int i=0;i<1000;i++) t+=ps[i]->f0+ps[i]->f1+ps[i]->f2; printf("%.17g\n",t); return 0; }
