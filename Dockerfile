# DevPit container image. The binary is prebuilt by goreleaser (the SPA is
# already embedded via go:embed, ADR-0010) and copied in — this Dockerfile does
# no compilation.
#
# Alpine, not scratch/distroless, on purpose (ADR-0023): TLS to the forges needs
# ca-certificates, and the HEALTHCHECK probe needs wget (busybox provides it) —
# distroless has neither. The image is deliberately dumb: no entrypoint
# scripting, no config generation.
FROM alpine:3.21

RUN apk add --no-cache ca-certificates \
    && addgroup -S devpit \
    && adduser -S -G devpit -H devpit \
    && mkdir -p /var/lib/devpit \
    && chown devpit:devpit /var/lib/devpit

COPY devpit /usr/bin/devpit

USER devpit
EXPOSE 7474

# Liveness probe (GET /up → 200). The container must bind all interfaces
# (listen: ":7474" in its config) for this loopback probe to reach it; the host
# still publishes loopback-only (-p 127.0.0.1:7474:7474), preserving ADR-0001.
# Exec form (no shell): wget exits non-zero on any non-2xx or connection error.
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD ["wget", "-q", "-O", "/dev/null", "http://127.0.0.1:7474/up"]

# Config is always required and mounted read-only; the DB volume is optional —
# the event store is a rebuildable cache (ADR-0023). The container config must
# set `listen: ":7474"` and a writable db_path (e.g. /var/lib/devpit/devpit.db).
ENTRYPOINT ["/usr/bin/devpit"]
CMD ["--config", "/etc/devpit/config.yaml"]
