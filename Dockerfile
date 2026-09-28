# syntax=docker/dockerfile:1
# Builds either service: docker build --build-arg APP=producer|consumer .
# The build stage runs on the host platform and cross-compiles, so multi-arch
# images build without emulation.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG APP=producer
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/app ./${APP}

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source=https://github.com/tomdong2010/good-gokafka
LABEL org.opencontainers.image.licenses=MIT
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]
