# Built for the k3s cluster on the Pi. The Makefile's build-pi target still
# produces a bare binary for the systemd deployment; both are the same code,
# and this file exists alongside it during the migration.

FROM --platform=$BUILDPLATFORM golang:1.26 AS build
WORKDIR /src

# No go.sum: this module has no dependencies at all, so there is nothing to
# download and nothing to cache.
COPY go.mod ./
COPY . .

ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/homepage .

# scratch: a static binary with its templates and stylesheet embedded needs
# nothing else. No shell, no libc, nothing to patch.
#
# Unlike weight-tracker this app has no wall-clock logic, so it needs neither
# tzdata nor a TZ setting.
FROM scratch

# The visit count is written here; the deployment mounts the host's existing
# /var/lib/homepage over it.
WORKDIR /var/lib/homepage

COPY --from=build /out/homepage /homepage

# Non-root by numeric UID — scratch has no /etc/passwd to name a user in.
USER 65532:65532

EXPOSE 8091
ENTRYPOINT ["/homepage"]
CMD ["-addr", ":8091", "-counter", "/var/lib/homepage/count.txt"]
