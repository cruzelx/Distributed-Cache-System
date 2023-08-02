# Base image builder
FROM golang:1.17-buster as builder

RUN mkdir -p /data

WORKDIR /app

RUN useradd -c "Master Server" -u 1001 master

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-linkmode external -extldflags -static" -tags netgo -o /master


# Master image
FROM scratch

COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /master /master

# RUN chmod +x /master
USER master

EXPOSE 8000

CMD ["/master"]