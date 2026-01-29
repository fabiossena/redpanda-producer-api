$env:ENVIRONMENT = "development"
$env:REDPANDA_BROKERS = "172.23.161.197:31092"
$env:REDPANDA_DEFAULT_TOPIC = "go-api-topic"
$env:LOG_LEVEL = "debug"
$env:PORT = "8088"
go run main.go