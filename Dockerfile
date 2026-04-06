FROM golang:1.23-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /frugal ./cmd/frugal

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /frugal /frugal
COPY config/models.yaml /config/models.yaml

ENV FRUGAL_CONFIG=/config/models.yaml
ENV FRUGAL_ADDR=:8080

EXPOSE 8080
CMD ["/frugal"]
