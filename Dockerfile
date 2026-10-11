# Cross-compile on the build host's arch so multi-arch builds don't run the
# Go toolchain under QEMU.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY assets.go ./
COPY internal ./internal
COPY web ./web
COPY certs ./certs
COPY cmd/control-center ./cmd/control-center
ENV CGO_ENABLED=0
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X github.com/dockette/viewdoc/internal/version.Version=$VERSION" -o /out/control-center ./cmd/control-center \
 && mkdir -p /out/certs

FROM gcr.io/distroless/static-debian13:nonroot
ARG VERSION=dev
LABEL org.opencontainers.image.version=$VERSION
COPY --from=build /out/control-center /control-center
# Mount point for TLS_CERT=generate with TLS_CERT_DIR=/certs, owned by nonroot
# so a fresh named volume mounted here inherits that ownership and the
# generated cert can be written.
COPY --from=build --chown=65532:65532 /out/certs /certs
EXPOSE 8080
ENTRYPOINT ["/control-center"]
