# Этап сборки
FROM debian:bookworm-slim AS builder

# Устанавливаем Go 1.25 и необходимые пакеты
RUN printf "deb http://ftp.ru.debian.org/debian bookworm main\n" > /etc/apt/sources.list && \
    apt-get -y update && \
    apt-get -y install --no-install-recommends \
        wget tar libaio1 alien rpm2cpio cpio ca-certificates git gcc && \
    wget -q https://go.dev/dl/go1.25.0.linux-amd64.tar.gz && \
    tar -C /usr/local -xzf go1.25.0.linux-amd64.tar.gz && \
    rm go1.25.0.linux-amd64.tar.gz && \
    rm -rf /var/lib/apt/lists/*

ENV PATH=/usr/local/go/bin:$PATH

# Скачиваем и устанавливаем Oracle Instant Client из RPM
RUN mkdir -p /tmp/oracle-install && \
    cd /tmp/oracle-install && \
    wget --no-check-certificate --no-cookies --header "Cookie: oraclelicense=accept-securebackup-cookie" \
        https://download.oracle.com/otn_software/linux/instantclient/1923000/oracle-instantclient19.23-basic-19.23.0.0.0-1.x86_64.rpm && \
    rpm2cpio oracle-instantclient19.23-basic-19.23.0.0.0-1.x86_64.rpm | cpio -idmv && \
    mkdir -p /usr/lib/oracle/19.23 && \
    mv usr/lib/oracle/19.23/client64 /usr/lib/oracle/19.23/ && \
    ln -sf /usr/lib/oracle/19.23/client64/lib/libclntsh.so.19.1 /usr/lib/libclntsh.so && \
    ln -sf /usr/lib/oracle/19.23/client64/lib/libocci.so.19.1 /usr/lib/libocci.so && \
    cd / && \
    rm -rf /tmp/oracle-install

# Настраиваем переменные окружения для Oracle Instant Client
ENV ORACLE_HOME=/usr/lib/oracle/19.23/client64
ENV LD_LIBRARY_PATH=/usr/lib/oracle/19.23/client64/lib:$LD_LIBRARY_PATH
ENV PATH=$ORACLE_HOME/bin:$PATH

WORKDIR /build

# Копируем go.mod и go.sum для кэширования зависимостей
COPY go.mod go.sum ./

# Загружаем зависимости
RUN go mod download && go mod verify

# Копируем исходный код
COPY . .

# Собираем приложение
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags="-w -s" \
    -o ./email-service \
    .

#--------------------

# Этап runtime
FROM debian:bookworm-slim

# Настраиваем репозиторий Debian и устанавливаем необходимые пакеты
RUN printf "deb http://ftp.ru.debian.org/debian bookworm main\n" > /etc/apt/sources.list && \
    apt-get -y update && \
    apt-get -y install --no-install-recommends \
        libaio1 ca-certificates tzdata && \
    rm -rf /var/lib/apt/lists/* && \
    apt-get clean

ENV TZ=Europe/Moscow
RUN ln -snf /usr/share/zoneinfo/${TZ} /etc/localtime && echo ${TZ} > /etc/timezone

# Копируем Oracle Instant Client из этапа сборки
COPY --from=builder /usr/lib/oracle/19.23/client64 /usr/lib/oracle/19.23/client64
COPY --from=builder /usr/lib/libclntsh.so /usr/lib/libclntsh.so
COPY --from=builder /usr/lib/libocci.so /usr/lib/libocci.so

# Настраиваем переменные окружения для Oracle Instant Client
ENV ORACLE_HOME=/usr/lib/oracle/19.23/client64
ENV LD_LIBRARY_PATH=/usr/lib/oracle/19.23/client64/lib:$LD_LIBRARY_PATH
ENV PATH=$ORACLE_HOME/bin:$PATH

# Создаем пользователя для запуска приложения
RUN groupadd -g 1000 appuser && \
    useradd -u 1000 -g appuser -m -s /bin/bash appuser

WORKDIR /app

# Копируем скомпилированный бинарник из этапа сборки
COPY --from=builder /build/email-service ./email-service
COPY --from=builder /build/settings/settings.ini.example ./settings/settings.ini.example

# Создаем директории для логов и конфигурации
RUN mkdir -p logs settings && \
    chmod 777 /app/logs && \
    chown -R appuser:appuser /app

# Создаем entrypoint скрипт для установки прав при запуске
RUN echo '#!/bin/bash\n\
if [ ! -d /app/logs ]; then\n\
  mkdir -p /app/logs\n\
fi\n\
chmod 777 /app/logs 2>/dev/null || true\n\
exec "$@"' > /app/entrypoint.sh && \
    chmod +x /app/entrypoint.sh && \
    chown appuser:appuser /app/entrypoint.sh

USER appuser

ENTRYPOINT ["/app/entrypoint.sh"]
CMD ["./email-service"]