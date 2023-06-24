FROM golang:1.17-alpine

RUN mkdir -p /data

WORKDIR /app

COPY . .

RUN go get .

RUN go build -o auxiliary .

RUN chmod +x auxiliary

CMD ["go","run","auxiliary"]