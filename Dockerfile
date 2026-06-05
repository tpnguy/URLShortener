FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o urlshortener .

FROM alpine:3.21
WORKDIR /app
COPY --from=builder /app/urlshortener .
EXPOSE 8080
CMD ["./urlshortener"]
