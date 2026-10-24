#include <stdio.h>
long long fib(long long n) { return n < 2 ? n : fib(n-1) + fib(n-2); }
int main(void) { printf("%lld\n", fib(37)); return 0; }
