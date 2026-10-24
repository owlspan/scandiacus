#include <stdio.h>
int main(void) { long long s=0; for (long long i=0;i<400000000;i++) s += (i*i)%7 + (i>>3); printf("%lld\n",s); return 0; }
