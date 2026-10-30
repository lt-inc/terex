# Прямая публикация из Clips — проверка 30.10.2026

Реализованы операторские `publication-options` и `publish`, отдельный manual target,
неизменяемый publication-only media snapshot и durable UUID → publication mapping.
В Clips и редакторе доступны выбор медиа/аккаунта/платформы/подписи/видимости,
явный просмотр и подтверждение, private по умолчанию, прогресс и история рядом
с клипом, безопасный повтор существующей записи и сверка неизвестного TikTok upload.
Файлы клиента/токены/произвольные media URL в запрос не входят.

Поток не создаётся и не включается; его pinned targets не меняются. Обычный
scheduler/attempt/lease/outbox/worker выполняет публикацию. Снимок публикации
не становится latest revision редактора и не одобряет его автоматически;
последующее сохранение, restore или автоматическое одобрение не подменяют снимок.
Storage/reset/source-filter/account/quota fences сохраняются.

Проверено локально:

- `go test ./main-be/...` с disposable PostgreSQL 18 — успешно, включая все
  repository integration tests (последний полный прогон postgres: 57.835 s).
- `go test ./proto/... ./main-be/... ./gpu-be/...`: proto и GPU пакеты прошли;
  первоначальный main-be прогон выявил зависимость двух тестовых часов от SQL
  `now()`, исправлены только fixtures, затем весь main-be прошёл отдельно.
- `go vet ./main-be/...` и `git diff --check` — успешно.
- FE `npm ci --no-audit --no-fund`, `npm test` — 42/42,
  `npm run typecheck`, `npm run build` — успешно; после последней правки
  обновления истории typecheck повторён успешно.

Критические сценарии новых DB tests: шесть одновременных submit → один ID,
новый UUID и OAuth alias не обходят существующий uncertain результат,
revision/version/lease fences, сохранение и одобрение следующих editor revisions,
инициализация legacy revision, платформенная eligibility, отсутствие подключения/
TikTok session/SHA, verified source при архивированном потоке, явное исключение
возраста на точный снимок, квота и недоступный storage. Проверены оба dispatch:
YouTube rendered и TikTok verified original через реальные repository leases.

Проверки используют отдельную локальную БД `terex_manual_test`; production DB,
серверы, credentials и реальные внешние публикации не использовались.
RabbitMQ delivery integration и опциональный Linux media smoke не включались.
Не выполнялись браузерная визуальная приёмка и live platform proof.

Ограничения:

- Миграция 024 обязательна до запуска новой версии; rollback удаляет созданные
  manual targets и их detached snapshots, поэтому требует оценки сохранённой
  истории и внешних публикаций оператором.
- Скачанный original materialize разрешён только для verified Twitch import с
  SHA/размером/длительностью. Прочие клипы используют готовый rendered-файл.
  Legacy rendered без SHA доступен YouTube; для TikTok UI требует verified source
  или новую media revision с рендером. Метаданные клиента не заменяют проверку файла.
- Любая существующая публикация на клип/аккаунт, включая cancelled/published,
  блокирует новую manual запись; deliberate repost не реализован. Восстановление
  использует существующую запись и её штатные проверки неизвестного результата.
- Private/public TikTok и Google OAuth должны быть проверены координатором на
  разрешённых тестовых аккаунтах после deploy; отсутствие внешнего ID не является
  доказательством отсутствия поста и не разрешает новую загрузку.

Для живой приёмки: применить миграцию и обновить main-be/FE; открыть скачанный клип,
просмотреть точный вариант, выбрать private аккаунт каждой платформы и отправить
один запрос, затем подтвердить ID/видимость и обновление статуса из worker.
Повторить тот же UUID после разрыва HTTP и убедиться в сохранении ID, без нового поста.

Дополнение после release review:

- Rebuild сборки исключает manual publications и их active attempts: queued-снимок,
  версия и метаданные сохраняются, исходная команда по-прежнему получает lease на тот же файл.
- Approval и flow scheduler не создают повторное назначение для manual-публикации
  того же клипа через алиас аккаунта с той же платформой и непустым remote ID.
  Другие назначения и платформы сохраняют прежнюю семантику; cancelled uniqueness не менялась.
- Manual YouTube submit использует общий `publicationmetadata.Validate` до создания
  снимка и очереди: заголовки длиннее 100 символов и angle brackets отклоняются.
- Clips page обрабатывает отказ getter `localStorage`, сохраняя доступность списка.
- Проверки патча на той же локальной PostgreSQL 18: postgres, workflow service,
  HTTP transport и publicationmetadata tests; `go vet ./main-be/...`; FE typecheck
  и 42/42 tests; `git diff --check`. Удалённые серверы и внешние публикации не использовались.
