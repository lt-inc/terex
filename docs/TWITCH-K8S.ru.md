# Twitch collector: развёртывание в существующем k3s

Twitch collector разворачивается тем же GitHub Actions workflow, что `terex-be`,
`terex-gpu-be` и `terex-fe`: сборка образа, отправка в
`registry.legendary-trio.me`, обновление Kubernetes и ожидание готовности.
Используется существующий namespace **`admin`**. Отдельный namespace с названием
`stage` эта конфигурация не создаёт. Базовые манифесты FE/main-be и
инфраструктуры остаются в репозитории `lt-inc/k3s`; здесь хранятся collector и
точечные изменения для интеграции с Terex.

Реальные ключи задаются в **Kubernetes Secrets**, а не в YAML с Deployment и не
в Docker image. Корневой `.env` используется локальным Compose; Kubernetes его
автоматически не читает. GitHub Actions Secrets `REGISTRY_USER`,
`REGISTRY_PASSWORD`, `KUBE_HOST`, `KUBE_TOKEN`, `KUBE_CA` нужны workflow для
реестра и кластера. Twitch credentials этому workflow передавать не нужно.

## 1. Подготовить существующие сервисы

Перед первым запуском должны работать PostgreSQL, RabbitMQ, MinIO и три
существующих приложения Terex. Проверьте конфигурацию целевого кластера:

| Соединение | Значение для текущей схемы |
|---|---|
| Collector → HTTP API Terex | `http://terex-be:8000` |
| Terex → collector | `http://terex-twitch-collector:8080` |
| Terex → GPU worker gRPC | `GPU_GRPC_ADDR=terex-gpu-be:9090` |
| PostgreSQL | `postgres.infra.svc:5432` |
| MinIO для серверных обращений | `minio.infra.svc:9000` |
| `gpu-be` → workflow gRPC Terex | `MAIN_COORDINATOR_ADDR=terex-be:9091` |

`terex-be-service.patch.yaml` сохраняет HTTP-порт 8000 и добавляет workflow gRPC
9091 в существующий Service. `gpu-service.yaml` создаёт внутренний Service
`terex-gpu-be:9090` с selector `app: terex-gpu-be`. Существующий GPU Deployment
должен действительно иметь эту метку на Pod; его создание, настройка GPU-хоста
и хранилищ остаются предварительным условием. `terex-gpu-be.patch.yaml` задаёт
`MAIN_COORDINATOR_ADDR=terex-be:9091` у существующего `gpu-be` для callbacks;
`WORKFLOW_GRPC_ADDR=:9091` у main-be задаёт адрес его listener. Оба backend получают
существующее подключение RabbitMQ из `terex-runtime`. Настройки PostgreSQL, MinIO,
сертификаты и размещение GPU worker эти patches сохраняют.

Для браузера нужен доступный HTTPS-адрес панели. У `main-be` задайте
`APP_PUBLIC_URL=https://<домен-панели>` без завершающего `/`. В Google OAuth client
зарегистрируйте `https://<домен-панели>/api/accounts/oauth/callback`; если
`GOOGLE_REDIRECT_URL` задан явно, его значение должно совпадать у `main-be` и
`gpu-be`. `GOOGLE_CLIENT_ID` и `GOOGLE_CLIENT_SECRET` остаются только у `gpu-be`.
Подробнее — [настройка публикаций](PUBLISHING.ru.md).

Проверьте доступность previews через `S3_PUBLIC_ENDPOINT`: внутреннее имя
`minio.infra.svc` браузер пользователя не разрешит. URL загрузки, выдаваемый
импортом, должен также быть достижим из collector. Доступ узлов текущего k3s к
приватному registry уже используется остальными сервисами; collector сохраняет
тот же способ. Для другого кластера при необходимости подключите его registry
Secret через `imagePullSecrets` в Deployment.

## 2. Вписать секреты в локальные файлы

В `deploy/k8s/twitch/secrets/` есть пять `.env.example`. Скопируйте их в файлы
с теми же именами без `.example` и заполните значения:

```powershell
Copy-Item deploy/k8s/twitch/secrets/operator.env.example deploy/k8s/twitch/secrets/operator.env
Copy-Item deploy/k8s/twitch/secrets/service-tokens.env.example deploy/k8s/twitch/secrets/service-tokens.env
Copy-Item deploy/k8s/twitch/secrets/collector.env.example deploy/k8s/twitch/secrets/collector.env
Copy-Item deploy/k8s/twitch/secrets/db-init.env.example deploy/k8s/twitch/secrets/db-init.env
Copy-Item deploy/k8s/twitch/secrets/runtime.env.example deploy/k8s/twitch/secrets/runtime.env
```

Эти `.env` игнорируются Git. Файлы примеров не содержат рабочих credentials.
Значения не вставляются в команды через `--from-literal` и не выводятся в CI.

| Файл | Kubernetes Secret | Что заполнить |
|---|---|---|
| `operator.env` | `terex-operator` | `ADMIN_USERNAME`, `ADMIN_PASSWORD`: вход в панель; одинаковые credentials используются FE и main-be |
| `service-tokens.env` | `terex-service-tokens` | `TEREX_SERVICE_TOKEN`, `COLLECTOR_SERVICE_TOKEN`: два разных случайных токена; этот Secret подключают main-be и collector |
| `collector.env` | `terex-twitch-collector` | `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET` своего Twitch application; `DATABASE_URL` отдельного пользователя коллектора |
| `db-init.env` | `terex-collector-db-init` | `PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER`, `PGPASSWORD` существующего административного подключения; `COLLECTOR_DB_PASSWORD` нового пользователя |
| `runtime.env` | `terex-runtime` | `RABBITMQ_URL`: полный URL существующего RabbitMQ с его credentials; один broker используют main-be и gpu-be |

В `RABBITMQ_URL` укажите существующего пользователя, пароль, virtual host и
правильный протокол `amqp`/`amqps`. Для текущей инфраструктуры hostname —
`rabbitmq.infra.svc`; пароль в URL должен быть корректно закодирован. Пример
структуры: `amqp://<пользователь>:<пароль>@rabbitmq.infra.svc:5672/`. Этот Secret
не подключается к collector: он взаимодействует с Terex через HTTP.

`PGDATABASE` — существующая БД, которой пользуется main-be: она не обязана
называться `main`. `PGUSER` должен иметь права создать/обновить роль
`twitch_collector` и её схему. Пример структуры подключения collector:

```text
postgres://twitch_collector:<COLLECTOR_DB_PASSWORD>@postgres.infra.svc:5432/<PGDATABASE>?sslmode=disable
```

Укажите фактический режим SSL вашего PostgreSQL. Для нового пароля удобна
случайная строка hex: она безопасно помещается в URL без дополнительного
кодирования. Пароль в `DATABASE_URL` и `COLLECTOR_DB_PASSWORD` должен быть одним
и тем же; hostname, port и database в обоих файлах также должны соответствовать.
`COLLECTOR_DB_PASSWORD` использует только инициализация, само приложение читает
`DATABASE_URL`.

Collector не получает административный пароль PostgreSQL, общие ключи MinIO
или Google OAuth credentials. Он читает и изменяет свою схему
`twitch_collector`; импорт материалов выполняется через HTTP API Terex.

## 3. Однократно создать Secrets и расширить права deployer

Команды выполняются администратором с kubeconfig именно целевого кластера.
Сначала проверьте текущий context:

```powershell
kubectl config current-context
kubectl get namespace admin
```

Создайте или обновите пять Secrets, передавая значения из файлов:

```powershell
kubectl create secret generic terex-operator -n admin --from-env-file=deploy/k8s/twitch/secrets/operator.env --dry-run=client -o yaml | kubectl apply -f -
kubectl create secret generic terex-service-tokens -n admin --from-env-file=deploy/k8s/twitch/secrets/service-tokens.env --dry-run=client -o yaml | kubectl apply -f -
kubectl create secret generic terex-twitch-collector -n admin --from-env-file=deploy/k8s/twitch/secrets/collector.env --dry-run=client -o yaml | kubectl apply -f -
kubectl create secret generic terex-collector-db-init -n admin --from-env-file=deploy/k8s/twitch/secrets/db-init.env --dry-run=client -o yaml | kubectl apply -f -
kubectl create secret generic terex-runtime -n admin --from-env-file=deploy/k8s/twitch/secrets/runtime.env --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f deploy/k8s/twitch/github-deployer-rbac.yaml
```

Дополнительный Role/RoleBinding привязан к существующим service accounts
`github-deployer` в namespaces `pipeline` и `warmup`. Он даёт workflow права
создавать и обновлять нужные Deployment, Service, ConfigMap и Job в `admin`,
а также проверять их готовность. Прямых прав `get/list/watch` для Secrets этот
Role не добавляет. Сами Secrets создаются вручную; workflow ссылается на них
через `secretKeyRef`.

Настройки доступа контейнеров к Secrets описаны в
[официальной документации Kubernetes](https://kubernetes.io/docs/tasks/inject-data-application/distribute-credentials-secure/).

## 4. Запустить обычное развёртывание

После слияния ветки в `master` запускается `.github/workflows/build-and-deploy.yml`.
Он собирает четыре приложения. Новый collector image:

```text
registry.legendary-trio.me/lt-inc/terex-twitch-collector:<полный 40-символьный SHA коммита>
```

Для collector используется конкретный commit tag, чтобы развёртывание не
зависело от изменения `latest`. Workflow:

1. Генерирует ConfigMap с актуальным `docker/collector-db-init.sh` и collector
   manifest с закреплённым image.
2. Обновляет ConfigMap, удаляет предыдущую init Job с ожиданием удаления,
   создаёт новую Job и ждёт успешного завершения до 300 секунд. При ошибке
   инициализации следующий этап не запускается.
3. Инициализация создаёт/обновляет отдельную DB role и схему, а затем проверяет
   подключение collector и отсутствие доступа к таблицам main-be.
4. Подключает операторский Secret к FE/main-be, служебные токены к main-be,
   внутренние адреса collector/GPU worker и probes; подключает RabbitMQ URL к
   main-be/gpu-be, добавляет workflow gRPC-порт в Service, создаёт GPU Service и
   настраивает адрес callbacks у gpu-be.
5. Применяет Deployment и Service collector, обновляет images существующих
   сервисов, перезапускает Deployment и проверяет завершение rollout всех четырёх
   приложений.

`workflow_dispatch` также доступен. Ручной запуск с выбранной веткой обновляет
**все четыре приложения в namespace `admin`**, поэтому это следует делать только
с намеренно выбранной версией; это не изолированное preview-развёртывание.

Генератор можно проверить локально без доступа к кластеру и без secrets:

```powershell
python scripts/render-twitch-deploy.py --image registry.legendary-trio.me/lt-inc/terex-twitch-collector:0123456789abcdef0123456789abcdef01234567
```

SHA в этой команде — только пример для проверки генератора, не существующий
образ. По умолчанию результат — `.twitch-deploy/db-init-configmap.json` и
`.twitch-deploy/collector.yaml`; каталог игнорируется Git. Требуется обычный
Python 3, дополнительные библиотеки не нужны. Для другого выходного каталога
используйте `--output`.

## 5. Проверить запуск и включить сбор

```powershell
kubectl -n admin get deployments,pods,services
kubectl -n admin rollout status deployment/terex-twitch-collector --timeout=300s
kubectl -n admin get jobs
kubectl -n admin get pods -l app=terex-gpu-be
kubectl -n admin get endpointslices -l kubernetes.io/service-name=terex-gpu-be
```

В панели войдите с credentials из `terex-operator`. В разделе Twitch должно
отображаться готовое подключение к collector и наличие Twitch credentials.
Подключите YouTube-аккаунт обычным OAuth Terex, выберите получателя, добавьте
сначала два Twitch-канала, сохраните настройки и включите сбор. Затем нажмите
«Проверить сейчас».

Успешная проверка обновляет её время и список найденных клипов. Для подходящего
кандидата последовательно появляются download, import и готовый Short с preview.
Если подходящих клипов нет, пустой результат сам по себе не является ошибкой.
После проверки материала одобрите конкретную готовую версию и сначала выполните
private публикацию. До одобрения публичного выпуска не происходит.

Collector имеет одну реплику и стратегию `Recreate`, работает с UID 10001.
Временные видео лежат в ограниченном по объёму дисковом `emptyDir` в `/tmp`;
очередь и состояние заданий сохраняются в PostgreSQL. При замене Pod временные
файлы исчезают, а незавершённое задание возобновляется из сохранённой очереди.
Service на 8080 доступен только внутри кластера; отдельный Ingress для collector
не требуется.

| Симптом | Что проверить |
|---|---|
| Init Job не завершилась | Адрес БД, `PGDATABASE`, административные права и соответствие двух collector DB паролей |
| Pod ожидает конфигурацию | Наличие всех пяти Secrets и точных имён ключей в `admin` |
| `ImagePullBackOff` | Наличие commit image в registry и доступ Kubernetes к приватному registry |
| Панель не видит collector | Service/Endpoint collector, служебные токены, `COLLECTOR_URL` у main-be |
| Twitch credentials отсутствуют или discovery завершается отказом | Client ID/secret собственного Twitch application |
| Import зависает на обработке | Доступ к upload URL, MinIO, RabbitMQ; endpoints GPU Service на 9090 и callbacks на 9091 между gpu-be и main-be |
| Preview недоступен в браузере | Публичный MinIO endpoint, HTTPS и маршрутизация панели |

## Остановка и замена секретов

Общая остановка в разделе Twitch прекращает проверки и новые скачивания,
сохраняя уже импортированные материалы. Она не отменяет вручную одобренные
публикации Terex; для них используйте календарь.

При замене ключей обновите соответствующие локальные `.env`, повторите команды
создания Secrets и запустите workflow. Переменные окружения работающих Pod не
меняются при обновлении Secret; их обновляет новый rollout. Два служебных токена
должны переключаться согласованно у main-be и collector. При смене collector DB
пароля сначала выключите сбор, обновите `DATABASE_URL` и
`COLLECTOR_DB_PASSWORD`, затем повторно выполните workflow с init Job и после
проверки включите сбор.

Обычное повторное развёртывание сохраняет схему, задания, импортированные
материалы и настройки в PostgreSQL. Init Job не сбрасывает очередь и не удаляет
таблицы; откат image также не является откатом данных.
