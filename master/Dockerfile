FROM golang:1.17-alpine

WORKDIR /app

COPY . .

RUN go get .

CMD ["go","run","."]