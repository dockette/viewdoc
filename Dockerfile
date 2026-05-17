FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY assets.go ./
COPY internal ./internal
COPY web ./web
COPY certs ./certs
COPY cmd/control-center ./cmd/control-center
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /out/control-center ./cmd/control-center

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/control-center /control-center
EXPOSE 8080
ENTRYPOINT ["/control-center"]
