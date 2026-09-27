# The railpack CLI for build pods: it writes the build plan that the
# railpack BuildKit frontend then builds. Bump VERSION together with
# RAILPACK_FRONTEND in deploy/rendimiento.yaml.
FROM alpine:3.20
ARG TARGETARCH
ARG VERSION=0.40.0
ARG SHA256_arm64=86c5a3db888c710b2014262de55fc87a56c85c4e96ba7cd10894e5df210a212b
ARG SHA256_amd64=52e5558f830b7349386e9448e04351d0834df041d4c31c20b7ec4d539f0d66d7
RUN set -eu; \
    case "$TARGETARCH" in arm64) a=arm64; sum=$SHA256_arm64 ;; amd64) a=x86_64; sum=$SHA256_amd64 ;; *) exit 1 ;; esac; \
    wget -qO /tmp/rp.tgz "https://github.com/railwayapp/railpack/releases/download/v${VERSION}/railpack-v${VERSION}-${a}-unknown-linux-musl.tar.gz"; \
    echo "$sum  /tmp/rp.tgz" | sha256sum -c -; \
    tar -xzf /tmp/rp.tgz -C /usr/local/bin railpack; \
    rm /tmp/rp.tgz; \
    railpack --version
