# News collector

Сервис собирает новости из RSS и Google News, группирует их в сюжеты, пишет по лучшему сюжету пост с помощью LLM и публикует его в Telegram-канал через бота. Управление целиком в панели: вкладка **News** (`/admin/news`).

Код: `news-collector/` (Go, слои `domain` → `service` → `repository`/`transport`). Состояние хранится в схеме `news_collector` основной БД Terex (как у stats-collector), файлового хранилища нет. Панель ходит в `main-be` (`/api/news/*`), он проксирует запросы во внутренний API collector с отдельным bearer-токеном.

## Как это работает

```
RSS / Google News → извлечение текста → дедуп → эмбеддинг → сюжеты (по потоку) → отбор → LLM-пост → апрув → Telegram
```

Каждый шаг работает от состояния в БД (`articles.status`: `new → extracted → ready | duplicate`), поэтому перезапуск ничего не теряет. Цикл раз в `NEWS_TICK_INTERVAL` (1 минута).

1. **Сбор.** Источники, у которых подошёл срок обновления и которые привязаны хотя бы к одному включённому потоку, скачиваются. Записи старше 72 часов пропускаются. Дубликат URL отсекается по хэшу нормализованного адреса (без `utm_*`, фрагментов, `/` в конце).
2. **Очистка.** Страница статьи загружается, основной текст выбирается по плотности абзацев; если не вышло, берётся описание из ленты. Почти одинаковые тексты (SimHash, расстояние ≤ 3) помечаются `duplicate`.
3. **Эмбеддинг.** OpenRouter `NEWS_EMBEDDING_MODEL`. Перепечатки (косинус ≥ 0,97) тоже помечаются `duplicate`. Без `OPENROUTER_API_KEY` статьи копятся в `extracted` и обрабатываются после появления ключа.
4. **Сюжеты (по каждому потоку отдельно).** Статья сравнивается с вектором темы потока: ниже `min_relevance` она отбрасывается. Иначе она присоединяется к ближайшему открытому сюжету (косинус ≥ `NEWS_CLUSTER_THRESHOLD`, 0,72) или создаёт новый. Вектор сюжета пересчитывается как среднее.
5. **Отбор.** Оценка сюжета: `2·ln(1+изданий) + 3·релевантность + свежесть`. Число **независимых изданий** важнее всего. Сюжет с меньшим числом изданий, чем `min_sources`, ждёт. Сюжет, похожий (≥ 0,85) на уже опубликованный за 7 дней, пропускается.
6. **Генерация.** В LLM уходят до 6 статей сюжета. Промпт требует писать только по фактам из источников, на языке потока, в заданном стиле и объёме; если материала мало, модель отвечает `skip`, и сюжет помечается пропущенным. Ссылки на издания добавляются автоматически, модель их не пишет.
7. **Публикация.** `by_approve`: черновик ждёт решения во вкладке «Посты». `auto`: пост сразу уходит в очередь. Отправка в Telegram с `parse_mode=HTML`, весь текст экранируется.

Посты равномерно распределяются по активным часам: интервал между ними не меньше `min_interval_min` и не меньше ¾ от (окно ÷ число постов в день). Черновики, висящие на ревью дольше 48 часов, переходят в `expired`. Статьи хранятся 14 дней.

## Панель

| Страница | Что делает |
|----------|------------|
| Потоки | Список потоков, включение/выключение, «Создать пост» (в обход расписания), счётчики, состояние LLM |
| Настройка потока | Тема, язык (ru/en), источники, канал, режим, расписание, пороги отбора, стиль, длина, модель; таблица открытых сюжетов с оценками |
| Посты | Модерация: правка заголовка и текста, «Опубликовать», «Запланировать», «Отклонить», повтор после ошибки |
| Источники | RSS/Atom ленты и поиск Google News по запросу; проверка ленты перед добавлением; «Загрузить сейчас» |
| Telegram-каналы | Подключение бота и канала, проверка прав, тестовое сообщение, замена токена |

**Подключение канала.** Создайте бота у @BotFather, добавьте его администратором канала с правом публиковать сообщения, в панели укажите токен и канал (`@name`, ссылку `t.me/…` или числовой id). Сервис проверяет токен, существование чата и права бота. Токен хранится в БД зашифрованным (AES-256-GCM, ключ `NEWS_SECRET_KEY`) и больше нигде не показывается.

## Настройка качества

- **Лишние новости в канале** → поднимите «Релевантность теме» (обычно 0,25–0,45). Релевантность каждого сюжета видна в таблице сюжетов потока.
- **Мало постов** → снизьте релевантность или «Мин. изданий в сюжете», добавьте источники.
- **Одна новость выходит дважды** → снизьте `NEWS_COVERED_THRESHOLD`; **разные события склеиваются** → поднимите `NEWS_CLUSTER_THRESHOLD`.
- Начинайте с `by_approve` и переходите на `auto`, когда черновики стабильно хороши.

## Метрики, логи и дашборды

**Дашборд Grafana «Terex News Collector»** (`/d/terex-news-collector`, папка Terex). На staging его источник `k3s/monitoring/dashboards/terex/terex-news-collector.json` (Prometheus + Loki + PostgreSQL), локальная версия только с логами лежит в `docker/grafana/provisioning/dashboards/terex/`. Одноимённый файл из k3s заменяет локальный при деплое. Ссылка на него есть в дашборде «Logs — All apps».

Что на нём:
- **Здоровье:** сервис доступен, время с последнего успешного цикла, наличие ключей LLM и шифрования, состояние БД, черновики на ревью и возраст самого старого.
- **Работа за сутки:** опубликовано, создано, отказы отправки, ошибки OpenRouter, источники с ошибкой, статьи без эмбеддинга.
- **Потоки событий:** посты по потокам, пропущенные сюжеты (`covered` / `model_skip`), статьи по этапам, загрузка лент и страниц, запросы и задержки OpenRouter и Telegram, длительность цикла, задержка от черновика до публикации, CPU и память пода.
- **Состояние (из PostgreSQL):** таблицы потоков, источников, каналов и последних постов с причинами ошибок.
- **Логи (Loki):** объём по уровням, ERROR, WARNING, события постов, ошибки лент, сбои цикла, INFO и полный лог.

**Метрики** отдаёт `GET /metrics` collector (без токена, только внутри кластера; через `main-be` он недоступен). Prometheus собирает их заданием `terex-news-collector` в `k3s/monitoring/prometheus.yml`.

| Метрика | Что показывает |
|---------|----------------|
| `news_feed_fetches_total{kind,result}`, `news_articles_ingested_total{kind}` | загрузка лент и новые статьи |
| `news_page_fetches_total{result}`, `news_articles_processed_total{outcome}` | страницы статей, этапы обработки и дубликаты |
| `news_llm_requests_total{kind,result}`, `news_llm_request_duration_seconds{kind}` | вызовы OpenRouter (эмбеддинги и генерация) |
| `news_posts_drafted_total{flow,mode}`, `news_stories_skipped_total{flow,reason}` | генерация и пропущенные сюжеты |
| `news_posts_published_total{flow}`, `news_post_send_failures_total{flow,outcome}`, `news_post_publish_delay_seconds` | публикация, отказы (`retry` / `failed`) и время от черновика до канала |
| `news_telegram_requests_total{method,result}`, `news_telegram_request_duration_seconds{method}` | вызовы Bot API (`rate_limited` — флуд-контроль) |
| `news_pipeline_runs_total{stage,result}`, `news_pipeline_duration_seconds{stage}`, `news_pipeline_last_success_timestamp_seconds{stage}` | циклы `pipeline` и `publish` |
| `news_posts{status}`, `news_articles{status}`, `news_sources{state}`, `news_channels{status}`, `news_flows{state}`, `news_open_stories{flow}` | состояние из БД на момент сбора (кэш 10 с, лимит запроса 3 с) |
| `news_oldest_pending_post_age_seconds`, `news_oldest_unembedded_article_age_seconds`, `news_last_post_published_timestamp_seconds{flow}` | возраст очередей и время последней публикации |
| `news_db_up`, `news_llm_ready`, `news_secret_key_ready` | готовность |

**Логи.** Формат logfmt (`LOG_FORMAT=text`), в Loki лейблы `namespace="admin"`, `app="terex-news-collector"` и `level`. Staging-дашборд выбирает логи по `app`, потому что promtail читает ConfigMap только при старте; после его перезапуска появится ещё и `service="news-collector"` (как у twitch-collector). Локально лейбл `service="news-collector"` есть сразу. Ключевые сообщения: `feed fetched` / `feed fetch failed` (`source`, `new_articles`), `post drafted` (`flow`, `post`, `cluster`, `status`), `post published`, `post not published` (`reason`, `retry`), `model skipped a story`, `pipeline pass failed`, `embedding failed`, `clustering failed`, `generation failed`. Логи с `trace_id` ведут в Tempo, если включён `OTEL_EXPORTER_OTLP_ENDPOINT`.

## Ограничения

- **Google News даёт заголовки.** Ссылки в выдаче Google зашифрованы, и в большинстве случаев раскрыть их до страницы издания не удаётся. Тогда у статьи есть только заголовок и название издания. Для качественных постов добавляйте прямые RSS-ленты изданий, а Google News используйте для поиска тем.
- Картинки в посты не добавляются.
- Эмбеддинги хранятся в обычной колонке `bytea`, сравнение идёт в памяти по статьям за последние 72 часа (расширение pgvector не нужно). Для одного канала этого достаточно; при росте до десятков тысяч статей в сутки понадобится индекс.
- Один экземпляр collector (`replicas: 1`): внутри процесса стоит блокировка цикла.

## Безопасность

- Адреса лент и статей вводит оператор, поэтому HTTP-клиент не подключается к loopback, приватным, link-local и CGNAT-адресам (в том числе по редиректам). Так нельзя дотянуться до PostgreSQL, API Kubernetes или metadata-сервисов.
- `/api/*` collector требует `Authorization: Bearer $NEWS_SERVICE_TOKEN`; токен без префикса `Bearer` и пустой токен конфигурации отклоняются.
- Ошибки Telegram пересобираются без URL, потому что токен бота входит в адрес запроса.

## REST API collector (`/api`, через `main-be` как `/api/news/*`)

| Область | Эндпоинты |
|---------|-----------|
| Состояние | `GET /status` |
| Источники | `GET/POST /sources`, `PATCH/DELETE /sources/{id}`, `POST /sources/test` (проверка ленты без сохранения), `POST /sources/{id}/fetch` |
| Каналы | `GET/POST /channels`, `PATCH/DELETE /channels/{id}`, `POST /channels/{id}/verify`, `POST /channels/{id}/test` |
| Потоки | `GET/POST /flows`, `GET/PATCH/PUT/DELETE /flows/{id}`, `POST /flows/{id}/generate`, `GET /flows/{id}/stories` |
| Посты | `GET /posts?status=&flow_id=&limit=`, `GET/PATCH /posts/{id}`, `POST /posts/{id}/approve` (`scheduled_at` необязателен), `POST /posts/{id}/reject`, `GET /posts/{id}/preview` |
| Статьи | `GET /articles?flow_id=&limit=` |
| Служебное | `GET /health` (без авторизации) |

`PATCH` меняет только присланные поля (например, `{"enabled": true}`).

## Переменные окружения

| Переменная | Где | По умолчанию |
|------------|-----|--------------|
| `DATABASE_URL`, `NEWS_SERVICE_TOKEN` | news-collector | обязательны |
| `NEWS_SECRET_KEY` | news-collector | пусто: токены ботов сохранить нельзя. 32 байта в base64 (`openssl rand -base64 32`) |
| `OPENROUTER_API_KEY` | news-collector | пусто: без него нет эмбеддингов и генерации |
| `OPENROUTER_MODEL`, `NEWS_EMBEDDING_MODEL` | news-collector | `deepseek/deepseek-v4.1-flash`, `openai/text-embedding-3-small` |
| `NEWS_TICK_INTERVAL` | news-collector | `1m` (не меньше 10s) |
| `NEWS_CLUSTER_THRESHOLD`, `NEWS_COVERED_THRESHOLD` | news-collector | `0.72`, `0.85` |
| `HTTP_ADDR`, `TELEGRAM_API_BASE` | news-collector | `:8080`, `https://api.telegram.org` |
| `LOG_FORMAT`, `LOG_LEVEL`, `OTEL_EXPORTER_OTLP_ENDPOINT` | news-collector | `text`, `info`, пусто (трейсинг выключен) |
| `NEWS_COLLECTOR_URL`, `NEWS_SERVICE_TOKEN` | main-be | пусто: вкладка News отвечает 503 |

## Запуск

Локально: `NEWS_SERVICE_TOKEN=… NEWS_SECRET_KEY=… OPENROUTER_API_KEY=… docker compose -f docker-compose.yml -f docker-compose.news.yml up --build`, панель на http://localhost:3000/admin/news.

Staging (k3s): манифесты `k3s/admin/terex-news-collector.yaml`, `terex-news-collector-infisical.yml`, ConfigMap `terex-news-collector-config`; образ `terex-news-collector` собирает CI. Порядок первого выката:

1. В Infisical (staging, путь `/`) создать `NEWS_SERVICE_TOKEN` (случайная строка) и `NEWS_SECRET_KEY` (`openssl rand -base64 32`). `OPENROUTER_API_KEY` и `TEREX_DATABASE_URL` уже есть. **`NEWS_SECRET_KEY` нельзя менять после подключения каналов** — сохранённые токены перестанут расшифровываться (тогда каналы нужно подключить заново).
2. `terraform apply` в `k3s/terraform` (создаст Deployment, Service, Secret-синхронизацию и обновит ConfigMap с `NEWS_COLLECTOR_URL`).
3. `kubectl rollout restart deployment/terex-be -n admin`: Kubernetes не перезапускает pod при появлении нового Secret для env.
4. Запустить CI (`build-and-deploy`), чтобы обновить образы.
5. Применить мониторинг (тот же `terraform apply` обновляет ConfigMap Prometheus, promtail и дашборды); перезапустить `prometheus` и `promtail`, если они не подхватывают ConfigMap сами.

## Тесты

```bash
cd news-collector && go test ./...
# сквозной пайплайн (нужна одноразовая БД; тест удаляет схему news_collector!)
NEWS_TEST_DATABASE_URL='postgres://postgres:pw@127.0.0.1:55432/postgres?sslmode=disable' go test ./internal/service/
cd main-be && go test ./internal/transport/http/
cd FE && npm test
```

Сквозной тест поднимает фейковые ленту, OpenRouter и Telegram и проходит весь путь: статьи → сюжет из двух изданий → черновик → правка → апрув → сообщение в Telegram; отдельно проверяются отказ Telegram (бот удалён из канала) и работа без ключа LLM.
