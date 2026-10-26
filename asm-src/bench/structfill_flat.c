#include <stdio.h>
#include <stdlib.h>
typedef struct { double x, y, vx, vy; } P;
int main(void) {
    double t = 0;
    for (int round = 0; round < 20; round++) {
        int cap = 4, n = 0;
        P *ps = malloc(cap * sizeof(P));
        for (int i = 0; i < 500000; i++) {
            if (n == cap) { cap *= 2; ps = realloc(ps, cap * sizeof(P)); }
            P *p = &ps[n];
            p->x = i; p->y = 0; p->vx = 1; p->vy = 0.5;
            n++;
        }
        for (int i = 0; i < n; i++) t += ps[i].x + ps[i].vy;
        free(ps);
    }
    printf("%.17g\n", t);
    return 0;
}
