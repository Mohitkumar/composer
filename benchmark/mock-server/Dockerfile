FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY main.go .
RUN go mod init mock-server && go build -o mock-server .

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /app/mock-server /mock-server
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --retries=5 CMD wget -qO- http://localhost:8080/health || exit 1
CMD ["/mock-server"]
