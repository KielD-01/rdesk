# rdesk-server: one static binary (signaling + TURN/STUN + browser viewer).
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/rdesk-server ./cmd/rdesk-server

FROM scratch
COPY --from=build /out/rdesk-server /rdesk-server
USER 65534:65534
EXPOSE 8080 3479/udp 3479/tcp
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s CMD ["/rdesk-server", "-health"]
ENTRYPOINT ["/rdesk-server"]
