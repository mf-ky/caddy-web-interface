# CaddyWeb container image.
#   docker compose up -d --build
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/caddyweb ./cmd/caddyweb

FROM alpine:3.20
# openssh-client + rsync are only used for the optional "copy backups with rsync" feature
RUN apk add --no-cache ca-certificates openssh-client rsync tzdata \
 && adduser -D -H -u 1000 caddyweb \
 && mkdir /data && chown caddyweb:caddyweb /data
COPY --from=build /out/caddyweb /usr/local/bin/caddyweb
USER caddyweb
ENV CADDYWEB_DATA=/data CADDYWEB_LISTEN=:8090
VOLUME /data
EXPOSE 8090
ENTRYPOINT ["caddyweb"]
CMD ["serve"]
