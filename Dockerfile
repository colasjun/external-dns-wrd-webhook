FROM golang:1.23.0-alpine3.20 AS builder
WORKDIR /app
COPY . /app

RUN apk --no-cache add make git && make build

FROM alpine:3.20

COPY --from=builder /app/external-dns-wrd-webhook /
ENTRYPOINT ["/external-dns-wrd-webhook"]
