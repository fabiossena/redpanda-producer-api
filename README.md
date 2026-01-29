# Redpanda Producer API
HTTP API service that receives messages and publishes to Redpanda. Built with Go for learning and testing Redpanda as a message broker.

Quick Start
Prerequisites
Go 1.21+

Docker (optional, for Redpanda)

1. Clone and setup
bash
git clone https://github.com/your-user/redpanda-producer-api.git
cd redpanda-producer-api

# Install dependencies
go mod download
go mod tidy

# Copy environment file
cp .env.example .env
2. Start with Docker (recommended)
bash
# Start complete stack (API + Redpanda)
docker-compose up -d

3. Or run locally without Docker
bash
# Start Redpanda first (if not using Docker)
docker run -d -p 9092:9092 -p 8981:8981 --name redpanda docker.redpanda.com/redpandadata/redpanda:latest

# Run the API
go run main.go
API Usage
Base URL: http://localhost:8980
Send a message
bash
# Basic message
curl -X POST http://localhost:8980/send \
  -H "Content-Type: application/json" \
  -d '{"value": "Hello Redpanda!"}'

# With specific topic
curl -X POST http://localhost:8980/send \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "user-events",
    "key": "user123",
    "value": "User logged in at 14:30"
  }'

# With headers
curl -X POST http://localhost:8980/send \
  -H "Content-Type: application/json" \
  -d '{
    "value": "Message with headers",
    "headers": {
      "app": "myapp",
      "env": "production"
    }
  }'
Other endpoints
bash
# Health check
curl http://localhost:8980/health

# List topics
curl http://localhost:8980/topics

# Metrics (Prometheus format)
curl http://localhost:8980/metrics
Scripts
Windows (PowerShell)
powershell
# Run complete local setup
.\run-local.ps1

# Test API endpoints
.\scripts\test-api.ps1
Linux/Mac
bash
# Make script executable
chmod +x run-local.sh

# Run setup
./run-local.sh
Configuration
Edit .env file:

env
PORT=8980
REDPANDA_BROKERS=localhost:9092
DEFAULT_TOPIC=default-topic
LOG_LEVEL=info
Monitoring
API: http://localhost:8980/health

Redpanda Console: http://localhost:8981

View messages: docker exec redpanda rpk topic consume default-topic --brokers localhost:9092

Project Structure
text
redpanda-producer-api/
├── main.go                 # Entry point
├── go.mod                  # Go dependencies
├── .env.example           # Environment template
├── Dockerfile             # Container build
├── docker-compose.yml     # Full stack
├── docker-compose.dev.yml # Development
├── run-local.ps1         # Windows setup script
├── run-local.sh          # Linux/Mac setup script
├── config/               # Configuration
├── api/                  # HTTP handlers
├── producer/             # Redpanda client
└── scripts/              # Utility scripts
Commands Summary
bash
# Development with hot reload
air

# Build Docker image
docker build -t redpanda-producer-api .

# Run tests
go test ./...

# Check code quality
go vet ./...
For issues or contributions, open a GitHub issue or PR.

