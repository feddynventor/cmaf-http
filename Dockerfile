FROM golang:1.24-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o api-server .

FROM alpine:3.19
WORKDIR /muxer

COPY --from=builder /app/api-server .

EXPOSE 8080

CMD ["./api-server"]
