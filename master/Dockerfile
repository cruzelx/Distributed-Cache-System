FROM golang:1.17-alpine

WORKDIR /app

COPY . .

RUN go get .

RUN go build -o master .

RUN chmod +x master

EXPOSE 8000

CMD ["go","run","master"]