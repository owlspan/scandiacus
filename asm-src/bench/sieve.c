#include <stdio.h>
#include <stdlib.h>
int main(void) { long long n = 50000000; char *c = calloc(n+1,1); long long count=0;
for (long long i=2;i<=n;i++) if(!c[i]){count++; for(long long j=i*i;j<=n;j+=i)c[j]=1;}
printf("%lld\n",count); return 0; }
