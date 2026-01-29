# 1. Baixar dependências
go mod download

# if necessary
go mod tidy

# 2. Executar
go run main.go

# 3. Testar a API
curl.exe -X POST http://localhost:8080/send -H "Content-Type: application/json" -d '{"value": "Minha primeira mensagem"}'

# Com tópico específico
curl -X POST http://localhost:8080/send -H "Content-Type: application/json" -d '{"topic": "logs", "key": "user123", "value": "Usuário logado às 10:30"}'

# Com headers
curl -X POST http://localhost:8080/send \
  -H "Content-Type: application/json" \
  -d '{
    "value": "Mensagem com headers",
    "headers": {
      "app": "myapp",
      "env": "production",
      "version": "1.0.0"
    }
  }'