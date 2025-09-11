# build app
FROM golang:1.24 AS builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN go build -o main ./cmd

# run app
FROM ubuntu:22.04

WORKDIR /app

COPY --from=builder /app/main .

EXPOSE 8443

CMD ["./main"]