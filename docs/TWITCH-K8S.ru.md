# Twitch collector на стейдже

Все Kubernetes-ресурсы Terex, включая Twitch collector, принадлежат
[lt-inc/k3s](https://github.com/lt-inc/k3s). Terraform создаёт Deployment,
Service, ConfigMap, InfisicalSecret, подготовку БД, ingress и права доступа.
Здесь находятся код четырёх приложений, Docker images и workflow их выпуска.
GitHub Actions только обновляет images существующих Deployment и ждёт rollout.

Инструкции по первому запуску и переходу с прежнего CI-развёртывания:
[runbook k3s](https://github.com/lt-inc/k3s/blob/master/docs/terex-twitch-rollout.md).
Применять инфраструктуру нужно с отдельным kubeconfig LT-Inc. Не используйте
случайный текущий Kubernetes context. CI должен оставаться остановленным на весь
промежуток Terraform `plan → apply`, иначе сохранённый plan может вернуть старый image.

## Секреты

Реальные значения хранятся в [Infisical](https://infisical.legendary-trio.me),
проект `lt-inc-secret-manager-1gm1`, окружение `staging`, путь `/`.
Kubernetes Operator переносит только явно объявленные поля в Secrets namespace
`admin`. Локальный `.env` нужен Docker Compose и не читается Kubernetes.

| Поля Infisical | Назначение |
|---|---|
| `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET` | Реальные credentials приложения из Twitch Developer Console |
| `COLLECTOR_DB_PASSWORD` | Пароль ограниченной роли `twitch_collector`; из него CR собирает collector `DATABASE_URL` |
| `TEREX_SERVICE_TOKEN` | Collector → main-be: чтение настроек и импорт |
| `COLLECTOR_SERVICE_TOKEN` | Main-be → collector: управление через панель |
| `TEREX_ADMIN_USERNAME`, `TEREX_ADMIN_PASSWORD` | Внутренние credentials FE → main-be, не второй пользовательский вход |
| `ADMIN_BASIC_AUTH_HTPASSWD_LINE_TEREX` | Существующий пользовательский вход через Traefik |
| `POSTGRES_USER`, `POSTGRES_PASSWORD` | Существующее административное подключение, доступно только Job подготовки БД |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | Google Web OAuth client для подключения нового YouTube-аккаунта; отдельный `terex-youtube-oauth` только у GPU worker |

Не заменяйте действующие значения при переходе. Генерировать можно только
отсутствующие служебные пароли и токены. Twitch credentials выдаёт провайдер.
Для нового `COLLECTOR_DB_PASSWORD` используйте случайную URL-safe строку;
например, hex от 32 случайных байт. Изменение Secret само по себе не меняет
пароль роли PostgreSQL и не перезапускает Pod.

Для подключения нового YouTube-аккаунта у Google Web OAuth client должен быть
разрешён redirect `https://admin.legendary-trio.me/api/accounts/oauth/callback`.
Google credentials нельзя сгенерировать: возьмите существующую пару в Google
Cloud Console. Они необязательны для старых jobs с уже сохранённым auth-файлом.
После первой синхронизации `terex-youtube-oauth` перезапустите GPU Deployment,
чтобы он прочитал env-секреты.

Collector получает собственный DSN, Twitch credentials и два служебных токена.
Общие MinIO credentials, YouTube credentials, RabbitMQ и административный
пароль PostgreSQL ему не передаются. Объекты загружаются через ограниченные
presigned URL импорта Terex.

## Вход и взаимодействие приложений

Оператор входит один раз через существующий Traefik Basic Auth на
`https://admin.legendary-trio.me`. При `FE_AUTH_MODE=traefik` панель принимает
признак проверенного входа от Traefik, а серверный `/api` proxy отправляет
собственные внутренние Basic credentials в main-be. Пользовательский
Authorization не пересылается. Сетевая политика разрешает обращения к FE только
от Traefik. Серверные credentials не добавляются в браузерные ответы или bundle.

Без Traefik используйте локальный `FE_AUTH_MODE=basic` с одинаковыми
`ADMIN_USERNAME` / `ADMIN_PASSWORD` у FE и main-be. Режим `traefik` нельзя
включать для самостоятельно выставленного в интернет FE.

| Соединение | Адрес |
|---|---|
| FE и collector → main-be HTTP | `http://terex-be.admin.svc:8000` |
| Main-be → collector | `http://terex-twitch-collector.admin.svc:8080` |
| Main-be → GPU worker | `terex-gpu-be.admin.svc:9090` |
| GPU worker → workflow callbacks | `terex-be.admin.svc:9091` |
| Collector → PostgreSQL | `postgres.infra.svc:5432/main`, роль/схема `twitch_collector` |

Collector обращается к main-be напрямую, обходя пользовательский ingress.
Его Bearer-токен не разрешает одобрение или публикацию. Collector Service не
имеет публичного ingress.

`APP_PUBLIC_URL=https://admin.legendary-trio.me` задаёт Origin и Google callback.
Точный GET `/api/accounts/oauth/callback` доступен без Basic Auth и проверяется
backend по одноразовому OAuth state. Остальные запросы требуют входа.
Настройка YouTube описана в [PUBLISHING.ru.md](PUBLISHING.ru.md).

## Выпуск и проверка

После подготовки k3s слияние Terex PR в `master` запускает сборку четырёх
образов и обновление `terex-be`, `terex-gpu-be`, `terex-fe`,
`terex-twitch-collector`. `workflow_dispatch` также обновляет все четыре
приложения на том же стейдже и не создаёт отдельную preview-среду.
Версии фиксируются тегом коммита; Terraform сохраняет текущий image при
изменении инфраструктуры. Изменение env-секретов требует отдельного rollout
из k3s, даже если тег приложения остался прежним.

Проверьте один вход в панель, состояние collector и наличие Twitch credentials.
Затем добавьте сначала два канала, выберите YouTube-аккаунт, сохраните настройки
и включите сбор. «Проверить сейчас» запускает discovery; подходящий клип проходит
download → import → render и появляется как готовый черновик с preview.
Пустая выборка сама по себе не означает ошибку. Публикация требует просмотра
и одобрения конкретной версии.

Подготовка БД — отдельный версионный Job k3s без TTL. Обычный выпуск приложения
его не пересоздаёт. При изменении скрипта или явной повторной инициализации
создаётся новая версия; данные и очередь не удаляются.

| Симптом | Проверка |
|---|---|
| Pod ожидает конфигурацию | Синхронизация InfisicalSecret и наличие требуемых ключей |
| Init Job завершилась ошибкой | Адрес PostgreSQL, база `main`, административные права и совпадение collector пароля |
| Внутренняя авторизация backend не работает | Совпадение `terex-operator` у FE и main-be; новый браузерный пароль вводить не нужно |
| Панель не видит collector | Service/endpoints, `COLLECTOR_URL` и согласованные служебные токены |
| Import не завершается | Upload URL, MinIO, RabbitMQ, GPU Service 9090 и workflow Service 9091 |
| Preview не открывается | Публичный MinIO endpoint, HTTPS и доступность presigned URL из браузера |

Остановка в разделе Twitch прекращает новые проверки и скачивания, сохраняя
импорты и очередь. Она не отменяет уже одобренные публикации: ими управляет
календарь Terex. При обновлении приложения состояние восстанавливается из БД;
временные файлы в ограниченном `emptyDir` могут быть скачаны заново.
