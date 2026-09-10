FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/vloader ./cmd/vloader
FROM alpine:3.23
RUN apk add --no-cache ca-certificates && addgroup -g 10001 vloader && adduser -D -u 10001 -G vloader vloader && mkdir -p /data /media && chown vloader:vloader /data /media
COPY --from=build /out/vloader /usr/local/bin/vloader
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["vloader"]
