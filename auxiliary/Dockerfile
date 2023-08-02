# Base image builder
FROM golang:1.17-buster as builder

RUN mkdir -p /data

WORKDIR /app

RUN useradd -c "Auxiliary Server" -u 1001 aux

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-linkmode external -extldflags -static" -tags netgo -o /auxiliary


#Aux image
FROM scratch

COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /auxiliary /auxiliary

# RUN chmod +x /auxiliary
USER aux

CMD [ "/auxiliary" ]