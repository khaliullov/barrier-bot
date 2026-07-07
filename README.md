# Barrier Bot 🚧

Telegram-бот для управления физическими шлагбаумами через SIP-телефонию. Разработан специально для работы с сервисом **Мегафон Мультифон**, поддерживает строгий RBAC (управление ролями), гостевые доступы и работу в IPv6-окружении.

## Основные возможности

*   **Управление через SIP**: Открытие шлагбаума простым звонком (signaling-only, без RTP).
*   **Гибкий доступ**: Постоянные пользователи, администраторы шлагбаумов и временные гости.
*   **Автоматическое истечение прав**: Доступ аннулируется автоматически по истечении заданного срока.
*   **SPA-интерфейс**: Бот работает в режиме "одного сообщения", обновляя текущее меню вместо спама новыми сообщениями.
*   **Безопасность**: Атомарная перезапись конфигурации (TOML), подробный аудит действий администраторов.
*   **NAT Traversal**: Поддержка `rport` и автоматическое определение публичного IP.

## Требования

*   Go 1.22+
*   Аккаунт **Мегафон Мультифон** (с установленным SIP-паролем).
*   Токен Telegram бота от [@BotFather](https://t.me/BotFather).

## Быстрый старт

1.  **Сборка:**
    ```bash
    go build -o barrier-bot ./cmd/bot/main.go
    ```

2.  **Настройка конфигурации:**
    Скопируйте `config.example.toml` в `config.toml` и заполните данные:
    *   `telegram_token`
    *   `master_admin_id` (ваш ID, можно узнать у [@userinfobot](https://t.me/userinfobot))
    *   Данные SIP (логин Multifon — это номер телефона без `+`)

3.  **Запуск:**
    ```bash
    ./barrier-bot -config config.toml
    ```

## Деплой на сервер (Systemd)

### 1. Подготовка системы
Создайте пользователя и директории:
```bash
sudo useradd -r -s /bin/false barrier-bot
sudo mkdir /etc/barrier-bot
sudo chown barrier-bot:barrier-bot /etc/barrier-bot
```

### 2. Установка бинарного файла
```bash
sudo mv barrier-bot /usr/local/bin/
sudo chmod +x /usr/local/bin/barrier-bot
```

### 3. Создание сервиса
Создайте файл `/etc/systemd/system/barrier-bot.service`:

```ini
[Unit]
Description=Barrier Bot - Telegram SIP Barrier Controller
Documentation=https://github.com/khaliullov/barrier-bot
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=barrier-bot
Group=barrier-bot

# Путь к конфигу
ExecStart=/usr/local/bin/barrier-bot -config /etc/barrier-bot/config.toml

# Перезапуск
Restart=on-failure
RestartSec=5s
StartLimitIntervalSec=60
StartLimitBurst=5

# === Security Hardening ===
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
RestrictRealtime=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
RestrictNamespaces=yes
SystemCallArchitectures=native

# Разрешаем запись ТОЛЬКО в директорию конфига (атомарная перезапись TOML)
ReadWritePaths=/etc/barrier-bot

# Сетевые разрешения (SIP UDP 5060 + HTTPS для Telegram API)
AmbientCapabilities=CAP_NET_BIND_SERVICE

# Логирование
StandardOutput=journal
StandardError=journal
SyslogIdentifier=barrier-bot

# Окружение
Environment=GOTRACEBACK=crash

[Install]
WantedBy=multi-user.target
```

### 4. Запуск
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now barrier-bot
```

### 5. Настройка Nginx (Проксирование)

Для доступа к веб-интерфейсу снаружи настройте Nginx как реверс-прокси. Добавьте следующий блок в конфигурацию вашего сайта:

```nginx
location /guest/ {
    proxy_pass http://127.0.0.1:8080/guest/;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;

    # WebSocket support
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "Upgrade";
}
```

## Управление

*   **/start** — Основное меню управления шлагбаумами.
*   **/admin** — Панель администратора (управление пользователями, админами и просмотр логов).
*   **/guest_access** — Быстрое создание гостевого пропуска на 24 часа.

## Архитектура ролей

1.  **Super Admin** (Master): Полный доступ, создание новых шлагбаумов, назначение других админов.
2.  **Barrier Admin**: Управление пользователями конкретного шлагбаума, выдача доступов на любой срок.
3.  **User**: Может открывать разрешенные шлагбаумы и создавать гостевые доступы на 24 часа.
4.  **Guest**: Может только открывать шлагбаум. Не может создавать гостевые доступы.

## Разработка

Проект использует `gosip` для работы с протоколом SIP и `telegram-bot-api` для взаимодействия с пользователем. Конфигурация хранится в TOML и обновляется атомарно (запись во временный файл + rename), что исключает повреждение данных при сбоях.
