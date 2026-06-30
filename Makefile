APP_NAME    := barrier-bot
BUILD_DIR   := dist
CONFIG_DIR  := /etc/barrier-bot
INSTALL_BIN := /usr/local/bin/$(APP_NAME)
UNIT_FILE   := deploy/barrier-bot.service

.PHONY: build-linux install uninstall clean run test

# Кросс-компиляция под Linux с оптимизациями
build-linux:
	@echo "==> Building $(APP_NAME) for linux/amd64..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-trimpath \
		-ldflags="-s -w -X main.Version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)" \
		-o $(BUILD_DIR)/$(APP_NAME) \
		./cmd/bot/main.go
	@echo "==> Binary size: $$(du -h $(BUILD_DIR)/$(APP_NAME) | cut -f1)"
	@echo "==> Done: $(BUILD_DIR)/$(APP_NAME)"

# Установка на Ubuntu сервер
install: build-linux
	@echo "==> Installing $(APP_NAME)..."
	# Создаём системного пользователя
	sudo useradd --system --no-create-home --shell /usr/sbin/nologin $(APP_NAME) || true
	# Создаём директорию конфига
	sudo mkdir -p $(CONFIG_DIR)
	sudo chown $(APP_NAME):$(APP_NAME) $(CONFIG_DIR)
	sudo chmod 750 $(CONFIG_DIR)
	# Копируем бинарник
	sudo cp $(BUILD_DIR)/$(APP_NAME) $(INSTALL_BIN)
	sudo chmod 755 $(INSTALL_BIN)
	# Копируем пример конфига (если конфига ещё нет)
	@if [ ! -f $(CONFIG_DIR)/config.toml ]; then \
		sudo cp config.example.toml $(CONFIG_DIR)/config.toml; \
		sudo chown $(APP_NAME):$(APP_NAME) $(CONFIG_DIR)/config.toml; \
		sudo chmod 640 $(CONFIG_DIR)/config.toml; \
		echo "==> Config copied to $(CONFIG_DIR)/config.toml — EDIT IT BEFORE STARTING"; \
	fi
	# Устанавливаем unit-файл
	sudo cp $(UNIT_FILE) /etc/systemd/system/
	sudo systemctl daemon-reload
	sudo systemctl enable $(APP_NAME)
	@echo ""
	@echo "==> Installation complete!"
	@echo "==> 1. Edit config:  sudo nano $(CONFIG_DIR)/config.toml"
	@echo "==> 2. Start:        sudo systemctl start $(APP_NAME)"
	@echo "==> 3. Check status: sudo systemctl status $(APP_NAME)"
	@echo "==> 4. View logs:    sudo journalctl -u $(APP_NAME) -f"

uninstall:
	sudo systemctl stop $(APP_NAME) 2>/dev/null || true
	sudo systemctl disable $(APP_NAME) 2>/dev/null || true
	sudo rm -f /etc/systemd/system/$(APP_NAME).service
	sudo rm -f $(INSTALL_BIN)
	sudo systemctl daemon-reload
	@echo "==> Uninstalled. Config dir $(CONFIG_DIR) preserved."

clean:
	rm -rf $(BUILD_DIR)

run:
	go run ./cmd/bot/main.go

test:
	go test -v ./...
