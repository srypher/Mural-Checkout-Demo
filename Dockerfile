FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o backend ./cmd/api

FROM gcr.io/distroless/base-debian12

WORKDIR /app
COPY --from=builder /app/backend ./backend

ENV PORT=8080
ENV RESET_ORDERS_ON_START=true
EXPOSE 8080

ENTRYPOINT ["/app/backend"]
