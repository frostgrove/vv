# storage

`github.com/frostgrove/vv/storage` — независимый от backend потоковый object
store, общий для filesystem и MinIO. У пакета нет внешних зависимостей, и он не
запускает фоновую работу.

## Что вы получаете

- `Put`, `Open`, `Head` и идемпотентный `Delete` по непрозрачным проверенным `Key`;
- явные режимы `CreateOnly` и `Replace`, причём zero value означает `CreateOnly`;
- ограниченные переносимые content type и metadata;
- `Stage`, `Promote`, `Abort` и `CleanupExpired` для загрузки до подтверждения UI-формы;
- один download-only `TemporaryURL` для filesystem и MinIO;
- переносимые классы ошибок для `errors.Is`.

Все входные reader принадлежат вызывающему коду: `Put` и `Stage` их не закрывают.
Body из `Open` всегда закрывает вызывающая сторона.

## Создание scoped store

Выберите backend и привяжите его к одному статическому логическому namespace:

```go
files, err := storage.New(storage.Config{
    Namespace: "avatars",
    Backend:   backend,
})
```

Namespace и key — не путь, bucket или URL. Key разбирается один раз на границе
приложения; его проверенное значение можно сохранить в доменной записи:

```go
key, err := storage.ParseKey("users/01J.../avatar.png")
info, err := files.Put(ctx, key, source, storage.PutOptions{
    ContentType: "image/png",
    Metadata: storage.Metadata{"classification": "avatar"},
})
```

По умолчанию запись создаёт только отсутствующий объект. Замена всегда явная:

```go
info, err = files.Put(ctx, key, source, storage.PutOptions{
    Mode: storage.Replace,
})
```

## Загрузка до подтверждения формы

Первый запрос стримит байты в приватный staging и возвращает непрозрачный ID:

```go
staged, err := files.Stage(ctx, upload, storage.StageOptions{
    ContentType: "image/png",
    ExpiresIn:   time.Hour,
})

// Верните staged.ID.Value() в UI и отправьте его вместе с итоговой формой.
```

`StageID` — чувствительное bearer-значение, а не доказательство принадлежности
upload текущему пользователю. Сохраняйте или связывайте его на сервере с
аутентифицированным actor/form и проверяйте эту связь перед `Promote` или
`Abort`. Storage сам не добавляет tenant- или authorization-policy.

После проверки и фиксации доменной формы значение разбирается, а уже загруженные
байты продвигаются в final key:

```go
stageID, err := storage.ParseStageID(form.UploadID)
info, err := files.Promote(ctx, stageID, finalKey, storage.PromoteOptions{})
```

Promotion тоже по умолчанию create-only. При коллизии final key staged upload
остаётся для повторной попытки или `Abort`. Очистку запускает scheduler
приложения ограниченными порциями; скрытой goroutine нет:

```go
result, err := files.CleanupExpired(ctx, storage.CleanupOptions{Limit: 100})
```

`Limit` ограничивает число успешных удалений, а не число просмотренных записей.
Передавайте maintenance-вызову отменяемый context с выбранным приложением
deadline, чтобы сканирование большого или медленного backend было ограничено и
по времени.

## Временные download-ссылки

У обоих адаптеров один вызов:

```go
link, err := files.TemporaryURL(ctx, key, storage.TemporaryURLOptions{
    ExpiresIn: 10 * time.Minute,
})
response.DownloadURL = link.URL()
```

`Link` — bearer capability. Обычное строковое представление скрыто; вызывайте
`URL()` только на границе ответа. Для filesystem нужны signer и HTTP handler из
[storagefs](storagefs.md). MinIO использует штатный pre-signed GET. TTL временной
ссылки задаётся целым числом секунд от одной секунды до семи дней, поэтому оба
signer имеют одинаковую переносимую семантику времени жизни.

## Ошибки и намеренные границы

```go
switch {
case errors.Is(err, storage.ErrNotFound):
case errors.Is(err, storage.ErrAlreadyExists):
case errors.Is(err, storage.ErrExpired):
case errors.Is(err, storage.ErrTemporary):
}
```

Текст ошибки содержит только операцию и ограниченный класс — без key, root,
bucket, endpoint, signed URL и сырой provider error. Причина доступна лишь через
контролируемую диагностику `errors.Is`/`errors.As`.

Здесь намеренно нет generic `List`, recursive delete, автоматических retry,
OTel, audit или tenant routing. Индекс, авторизация и выбор tenant остаются в
приложении; optional-интеграции могут оборачивать `Store`.

## См. также

- [storagefs](storagefs.md) — локальный filesystem backend и handler подписанных ссылок
- [storageminio](storageminio.md) — MinIO SDK adapter в отдельном Go module
- [storage roadmap](../../roadmaps/2026-08-26-1558-storage-roadmap.md) — обоснование контракта и отложенные capabilities
