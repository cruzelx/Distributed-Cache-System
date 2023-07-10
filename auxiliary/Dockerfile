FROM golang:1.17-alpine

RUN mkdir -p /data

WORKDIR /app

COPY . .

RUN go mod download

RUN CGO_ENABLED=0 GOOS=linux go build -o /auxiliary

RUN chmod +x /auxiliary

CMD [ "/auxiliary" ]