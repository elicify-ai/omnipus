/* Test-only Linux target. No product code is linked to this probe.
 * Compile without optimization so the stack probe cannot become a loop.
 */
#define _GNU_SOURCE
#include <errno.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/prctl.h>
#include <sys/resource.h>
#include <unistd.h>

__attribute__((noinline)) static unsigned stack_probe(unsigned depth) {
    volatile unsigned char page[16384];
    page[0] = (unsigned char)depth;
    page[sizeof(page)-1] = (unsigned char)(depth+1);
    unsigned next = stack_probe(depth+1);
    return next + page[0] + page[sizeof(page)-1];
}

int main(int argc, char **argv) {
    struct rlimit data, stack, address;
    int death = 0;
    setvbuf(stdout, NULL, _IONBF, 0);
    if (argc != 2 || getrlimit(RLIMIT_DATA, &data) ||
        getrlimit(RLIMIT_STACK, &stack) || getrlimit(RLIMIT_AS, &address) ||
        prctl(PR_GET_PDEATHSIG, &death)) return 80;
    printf("{\"data_soft\":%llu,\"data_hard\":%llu,\"stack_soft\":%llu,\"stack_hard\":%llu,\"as_soft\":%llu,\"as_hard\":%llu,\"parent_death\":%d}\n",
        (unsigned long long)data.rlim_cur, (unsigned long long)data.rlim_max,
        (unsigned long long)stack.rlim_cur, (unsigned long long)stack.rlim_max,
        (unsigned long long)address.rlim_cur, (unsigned long long)address.rlim_max,
        death);
    if (!strcmp(argv[1], "limits")) return 0;
    if (!strcmp(argv[1], "stack")) return (int)stack_probe(1);
    if (strcmp(argv[1], "malloc") && strcmp(argv[1], "mmap")) return 81;
    const size_t step = 8u << 20;
    size_t allocated = 0;
    for (;;) {
        errno = 0;
        void *p = !strcmp(argv[1], "malloc") ? malloc(step) :
            mmap(NULL, step, PROT_READ|PROT_WRITE, MAP_PRIVATE|MAP_ANONYMOUS, -1, 0);
        if (!p || p == MAP_FAILED) {
            printf("ALLOCATED=%zu ERRNO=%d\n", allocated, errno);
            return errno == ENOMEM ? 42 : 82;
        }
        /* Touch each page: reserve-only allocation is not this instrument. */
        for (size_t i = 0; i < step; i += 4096) ((volatile unsigned char *)p)[i] = 1;
        allocated += step;
        if (allocated > (2ull << 30)) return 83; /* guard: the limit did not bite */
    }
}
