FROM golang:1.21-alpine

WORKDIR /app

# Copiar tudo
COPY . .

# Primeiro garantir que todas as dependências estão resolvidas
RUN go mod tidy && \
    go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -o redpanda-producer-api

EXPOSE 8088

CMD ["./redpanda-producer-api"]