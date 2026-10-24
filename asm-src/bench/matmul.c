#include <stdio.h>
#include <stdlib.h>
int main(void){int n=400; double*a=malloc(8*n*n),*b=malloc(8*n*n),*c=calloc(n*n,8);
for(int i=0;i<n*n;i++){a[i]=(i%13)*0.5;b[i]=(i%7)*0.25;}
for(int i=0;i<n;i++)for(int k=0;k<n;k++){double x=a[i*n+k];for(int j=0;j<n;j++)c[i*n+j]+=x*b[k*n+j];}
double t=0;for(int i=0;i<n*n;i++)t+=c[i];printf("%.17g\n",t);return 0;}
