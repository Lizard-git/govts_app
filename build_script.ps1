New-Item -ItemType Directory -Force bin | Out-Null

# Клиент для Windows
go build -o bin/govts.exe ./cmd/client2

# Сервер для Linux (с повышением версии)
& ./scripts/build-server.ps1

# npm run dev:desktop
