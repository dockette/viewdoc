# Cross-compile on the build host's arch so multi-arch builds don't run the
# Go toolchain under QEMU.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod ./
COPY assets.go ./
COPY internal ./internal
COPY web ./web
COPY certs ./certs
COPY cmd/control-center ./cmd/control-center
ENV CGO_ENABLED=0
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/control-center ./cmd/control-center

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/control-center /control-center
EXPOSE 8080
ENTRYPOINT ["/control-center"]
