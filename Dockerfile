FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/harbor-cleaner ./cmd/harbor-cleaner

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/harbor-cleaner /usr/local/bin/harbor-cleaner
COPY configs/example.yml /configs/example.yml
WORKDIR /
ENTRYPOINT ["harbor-cleaner"]
