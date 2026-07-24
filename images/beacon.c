/*
 * beacon: from inside the guest, send a monotonically increasing sequence number
 * over UDP to a collector as fast as a fixed interval allows. A collector on the
 * host measures the gap between received sequence numbers across a migration =
 * the user-visible service blackout. One persistent socket, no per-packet fork.
 */
#include <arpa/inet.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>

int main(int argc, char **argv) {
    const char *ip = argc > 1 ? argv[1] : "172.20.0.1";
    int port = argc > 2 ? atoi(argv[2]) : 9999;
    long interval_us = argc > 3 ? atol(argv[3]) : 1000;

    int fd = socket(AF_INET, SOCK_DGRAM, 0);
    if (fd < 0) { perror("socket"); return 1; }

    struct sockaddr_in dst;
    memset(&dst, 0, sizeof(dst));
    dst.sin_family = AF_INET;
    dst.sin_port = htons(port);
    inet_pton(AF_INET, ip, &dst.sin_addr);

    struct timespec ts = {0, interval_us * 1000};
    char buf[32];
    unsigned long seq = 0;
    for (;;) {
        int n = snprintf(buf, sizeof(buf), "%lu", seq++);
        sendto(fd, buf, n, 0, (struct sockaddr *)&dst, sizeof(dst));
        nanosleep(&ts, NULL);
    }
    return 0;
}
