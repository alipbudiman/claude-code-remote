# Railway image for the M5.1 relay hub ONLY (the desktop server runs on the
# user's PC, not in the cloud). Multi-stage: build a static relay binary, then
# ship it on a bare alpine runtime.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/relay ./cmd/relay

FROM alpine:3.20
COPY --from=build /out/relay /relay
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/relay"]
