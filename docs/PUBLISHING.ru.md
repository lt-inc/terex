# Публикации, версии клипов и YouTube

`main-be` хранит расписания, одобрения, attempts, leases, checkpoints и outbox в PostgreSQL.
`gpu-be` хранит OAuth credentials в служебном bucket `credentials`, читает видео из MinIO,
рендерит версии и обращается к провайдеру. FE и RabbitMQ не получают токены.

## Настройка Google Cloud

Для текущего локального запуска используется отдельный проект **Terex Local**
(`terex-local-510513`). Будущий адрес — `https://terex.legendary-trio.me`;
DNS и развёртывание на этом домене пока не настраиваются.

1. Создайте Google Cloud project и включите **YouTube Data API v3**.
2. Настройте OAuth consent screen. Для режима Testing добавьте тестовых пользователей,
   которыми будете подключать каналы. Запрашиваемый scope —
   `https://www.googleapis.com/auth/youtube.force-ssl`.
3. Создайте OAuth client типа **Web application**.
4. Добавьте Authorized redirect URI:
   `APP_PUBLIC_URL` + `/api/accounts/oauth/callback`.
   Локальный пример: `http://localhost:3000/api/accounts/oauth/callback`.
   Для будущего домена: `https://terex.legendary-trio.me/api/accounts/oauth/callback`.
   URI должен точно совпадать с отправляемым параметром: схема, hostname, port, path.
   Authorized JavaScript origins для текущего серверного code + PKCE flow не нужны:
   обмен authorization code выполняет `gpu-be`, а браузер только следует redirect.
5. Передайте `GOOGLE_CLIENT_ID` и `GOOGLE_CLIENT_SECRET` только сервису `gpu-be`.
   Укажите публичный URL в `APP_PUBLIC_URL` у `main-be`.
   Если используете `GOOGLE_REDIRECT_URL`, задайте одинаковое значение обоим сервисам.
6. Создайте `.env` в корне репозитория по образцу `.env.publishing.example`.
   Docker Compose автоматически читает `.env`; сам файл примера не загружается.
   Задавайте `APP_PUBLIC_URL` без завершающего `/`. Перезапустите сервисы, откройте аккаунты,
   создайте получателя YouTube и нажмите подключение Google. После callback сверяйте
   реальный channel ID и подтвердите канал в интерфейсе. Переподключение другого канала
   к существующему аккаунту отклоняется.

### Импорт скачанного Web client JSON

Скачайте JSON OAuth client типа Web application **при его создании** в Google Cloud:
client secret показывается и доступен для скачивания только тогда. В этом клиенте
сразу регистрируются оба callback — локальный и будущего домена. Сохраните исходный JSON;
не рассчитывайте на повторное скачивание secret позже.
Файл содержит client secret: оставьте его в локальной папке загрузок, не добавляйте
в репозиторий и не вставляйте содержимое в чат или логи. Из корня репозитория выполните:

```powershell
.\scripts\Import-GoogleOAuth.ps1 -ClientJsonPath 'C:\Users\YOUR_USER\Downloads\client_secret.json'
```

Импортер проверяет объект `web`, наличие credentials и точного локального callback
в `web.redirect_uris`. Он сохраняет остальные настройки `.env` и записывает только
`GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URL`, не выводя их значения.
`.env` игнорируется Git. При необходимости другой файл задаётся параметром `-EnvPath`;
его защиту от Git обеспечьте отдельно.

После импорта Docker Compose должен заново создать контейнеры с изменённым environment:

```powershell
docker compose up -d --build
```

Callback приходит на FE `localhost:3000`, который проксирует `/api/*` в `main-be:8000`.
Для Google регистрируется адрес FE, порты gRPC `9090` и `9091` используются внутри стека.
Не выводите `docker compose config` или содержимое `.env` в общие логи: там могут быть секреты.

При будущем переходе на домен проверьте, что в Web client зарегистрирован точный HTTPS
callback. После готовности HTTPS и маршрутизации смените `APP_PUBLIC_URL`
на `https://terex.legendary-trio.me` и используйте тот же исходный JSON, сохранённый
при создании клиента, с явным адресом:

```powershell
.\scripts\Import-GoogleOAuth.ps1 -ClientJsonPath 'C:\Users\YOUR_USER\Downloads\client_secret.json' -RedirectUri 'https://terex.legendary-trio.me/api/accounts/oauth/callback'
```

Если credentials уже импортированы, можно вместо повторного импорта изменить только
`APP_PUBLIC_URL` и `GOOGLE_REDIRECT_URL` в `.env`, сохранив ID и secret; это также подходит,
если callback был добавлен в Google Cloud позже и отсутствует в исходном JSON.
Сейчас оставьте локальный callback и `APP_PUBLIC_URL=http://localhost:3000`.

Offline flow сохраняет refresh token; reconnect без нового refresh token сохраняет прежний.
`invalid_grant` блокирует публикации до переподключения. В Testing у внешнего consent screen
могут действовать короткие сроки refresh token; это учитывайте при длительных проверках.
[Документация OAuth](https://developers.google.com/identity/protocols/oauth2/web-server).

Google может ограничить upload нового неаудированного YouTube API project режимом private.
Такой ролик не считается опубликованным публично: Terex сверяет результат через API.
Аудит YouTube API project и OAuth verification — отдельные процедуры.
[Ограничение videos.insert](https://developers.google.com/youtube/v3/docs/videos/insert).

## Порядок работы

1. Для каждого получателя выберите account ID, начало и интервал в минутах, часах или днях.
   API хранит UTC timestamp (`start_at`) и `interval_seconds`. В интерфейсе можно менять
   IANA timezone (по умолчанию `Asia/Tomsk`), сохраняя момент выполнения.
2. Для обычной задачи используется заданное начало. У monitoring template пустое начало
   закрепляется по первому одобрению клипа этой задачи; при auto-approve — по готовности.
   Следующие клипы получают начало + индекс × интервал.
3. Одобрите готовую версию. Upload начинается в назначенное время; файл сначала private.
   В редакторе выберите итоговую видимость: public, unlisted или private. Перед выпуском проверяются processing, одобрение и отмена. Результат сверяется
   отдельным API-вызовом. Передача, обработка и выпуск имеют собственные timestamps.
4. В календаре доступны неделя/месяц, фильтры, история, перенос, отмена, повтор и запуск сейчас.
   Перенос меняет только одну публикацию одного аккаунта; начатый upload перенести нельзя.
5. В редакторе сохраняйте metadata, границы и строки субтитров. Время строк относится к
   оригиналу: рендер пересекает строки с диапазоном и сдвигает начало. Новый media object
   никогда не заменяет файл одобренной версии. Только metadata переиспользует готовый файл.
6. Сохранение клипа удерживает ещё не начатые публикации до одобрения новой версии.
   Начатые uploads и опубликованные версии остаются закреплены за прежней версией.
   Если оригинал отсутствует, доступны только metadata.
   Ошибка рендера позволяет повторить обработку или скопировать прежнюю готовую версию
   в новую, которую также потребуется одобрить. Хештеги YouTube добавляются в описание
   и в поисковые tags; общий лимит описания проверяется после добавления хештегов.

После простоя разрешённые просроченные планы догоняются. Старый backlog, перенесённый из
stub-процесса, получает `needs_attention` и требует ручного подтверждения в календаре.
Обычная отмена опубликованного ролика не удаляет его на YouTube. Внешний запрос выпуска,
который уже выполняется, невозможно атомарно отменить вместе с локальной транзакцией.

### Ручной выпуск устаревшего Twitch-клипа

В календаре откройте остановленный выпуск и отметьте исключение из правил актуальности.
«Назначить время» возвращает готовую, автоматически одобренную Twitch-версию в расписание.
«Upload now» назначает загрузку этой версии на текущий момент, игнорируя несохранённую дату
в поле. Для нового времени нужно подтвердить исключение заново, даже если оно уже было
сохранено для прежней даты. Изменение версии также отменяет прежнее подтверждение.
Исключение снимает только ограничения возраста и горизонта: оно не восстанавливает
удалённый файл и не обходит фильтры, паузу потока, квоты или состояние подключения YouTube.

## Восстановление upload

Новая session URI записывается в PostgreSQL до отправки файла. После разрыва связи worker
спрашивает серверную позицию и продолжает прежнюю session. Внешний video ID сохраняется
сразу после получения и используется для дальнейшего `Inspect`.

Потерянный ответ на последний chunk — отдельный случай. Если Google уже принял видео,
повторный запрос к session может вернуть его ID. Если session недоступна, Terex переводит
публикацию в `needs_attention`: повторный `videos.insert` автоматически не вызывается.
Проверьте канал вручную перед дальнейшим действием.
[Протокол resumable upload](https://developers.google.com/youtube/v3/guides/using_resumable_upload_protocol).

## Внутренние интерфейсы и будущие провайдеры

Версионированные очереди: `gpu.workflow.v1.publish`, `gpu.workflow.v1.render`. Сообщения
содержат ID, attempt ID и expected version; актуальный snapshot выдаёт gRPC coordinator.
RPC `Claim`, `Renew`, `Checkpoint`, `Complete`, `Fail` используют fencing token.
Outbox публикует persistent commands с подтверждением RabbitMQ. Повтор уведомления безопасен:
PG разрешает только одну активную попытку. Истёкший lease восстанавливается scheduler.

Provider capabilities описывают resumable transfer, получение по URL, отдельный выпуск и
доступность отмены. Финализация и отмена являются дополнительными интерфейсами. Для нового
провайдера нужно реализовать adapter, credential flow и зарегистрировать capabilities;
разрешения платформы проверяются отдельно. В этом релизе реализован только YouTube.

## Проверка

Обычные команды из корня (Go 1.26 и Node установлены):

```powershell
go test ./proto/... ./main-be/... ./gpu-be/...
cd FE
npm test
npm run typecheck
npm run build
```

Для repository integration tests задайте `TEST_DATABASE_URL` на отдельную тестовую БД;
для проверки confirmed delivery — `TEST_AMQP_URL` на тестовый RabbitMQ. Тесты не должны
использовать рабочую БД. Media smoke запускается с `MEDIA_SMOKE=1` в Linux image с ffmpeg
и fonts-liberation; `docker/Dockerfile.media-smoke` использует пакеты production base Ubuntu.
Сам production GPU base указан в `gpu-be/Dockerfile`; его доступность проверяется отдельно.

Live-приёмка требует Google project и тестового канала. Проверьте private upload, video ID,
processing и public verification, прерывание worker посреди передачи, reconnect, два
разных расписания и отмену перед выпуском. Публичная приёмка проводится после снятия
ограничений проекта. Автоматические tests используют HTTP mock YouTube и не доказывают
наличие прав у конкретного Google project.

Результаты локальной проверки и оставшиеся шаги приёмки: [VERIFICATION.ru.md](VERIFICATION.ru.md).
