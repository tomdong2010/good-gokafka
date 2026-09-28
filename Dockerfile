# syntax=docker/dockerfile:1
# Builds either service: docker build --build-arg APP=producer|consumer .
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG APP=producer
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./${APP}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]
