FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/files ./cmd/files

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -g 10001 files && adduser -D -u 10001 -G files files \
    && mkdir -p /var/lib/files/secrets && chown -R files:files /var/lib/files && chmod 700 /var/lib/files/secrets
COPY --from=build /out/files /usr/local/bin/files
USER files
ENV SERVICE_PORT=8080 SECRETS_DIR=/var/lib/files/secrets
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s CMD wget -q -O /dev/null http://127.0.0.1:8080/readyz || exit 1
ENTRYPOINT ["/usr/local/bin/files"]
