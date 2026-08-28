# storagefs

`github.com/frostgrove/vv/storage/storagefs` реализует `storage.Backend` в явно
заданном локальном каталоге, используя только стандартную библиотеку.

## Создание

```go
backend, err := storagefs.New(&storagefs.Config{
    Root: "/srv/app-data",
})
if err != nil { return err }
defer backend.Close()

avatars, err := storage.New(&storage.Config{
    Namespace: "avatars",
    Backend:   backend,
})
```

`Root` обязан быть абсолютным. Backend открывает его через `os.OpenRoot`, поэтому
вложенный symlink не выводит проверенную операцию наружу, а последующий `chdir`
не меняет дерево. Безопасные defaults: `0600` для файлов и `0700` для каталогов;
их можно заменить через `FileMode` и `DirMode`, не разрешая group/world write.
Настроенный root должен быть настоящим каталогом с owner-доступом и без
group/world write. Считайте его эксклюзивным доверенным деревом адаптера:
containment не выпускает операции наружу, но процесс, которому разрешено менять
записи внутри root, всё ещё может подменить объект внутри этого root.
Адаптер поддерживается на Unix-family targets и требует local filesystem с
atomic hard-link и rename в пределах одной FS. На Windows, Plan 9, js/wasm и
WASI constructor возвращает `storage.ErrUnsupported`; network filesystem и bind
mount с более слабыми semantics не входят в заявленную atomicity.

Объекты лежат в приватном версионированном формате. Не собирайте путь к нему и
не раздавайте файлы напрямую: формат атомарно объединяет bytes, content type и
metadata. Запись стримится во временный файл в том же root. `CreateOnly`
использует атомарное exclusive placement, `Replace` — atomic rename; ошибка или
отмена source не показывает частичный final object. Физические key segments,
StageID и work names кодируются lowercase base32, поэтому разные logical IDs не
сливаются на case-folding Unix filesystem. `Sync` сбрасывает полный
файл и leaf-каталог назначения перед успехом, но не делает sync всех только что
созданных родительских каталогов. Поэтому это не абсолютная гарантия crash
durability для первой записи в новый key path. Если различие важно, заранее
создайте дерево каталогов или добавьте подходящие deployment-specific меры.

## Временные HTTP-ссылки

Укажите публичный URL handler и случайный секрет длиной не менее 32 байт:

```go
backend, err := storagefs.New(&storagefs.Config{
    Root:       "/srv/app-data",
    BaseURL:    "https://files.example.com/download",
    SigningKey: signingKey,
    MaxLinkTTL: time.Hour,
})

downloadMux.Handle("/download", backend.Handler())
```

Теперь `Store.TemporaryURL` возвращает URL с HMAC по namespace/key/expiry.
Handler проверяет подпись и срок, затем открывает приватный объект, возвращает
сохранённый content type и никогда не раскрывает физический путь. Он принудительно
задаёт download disposition, `nosniff`, sandbox CSP и no-referrer headers.
Размещайте `downloadMux` на отдельном cookie-less origin, а не на авторизованном
origin приложения: headers дают defense in depth, но не заменяют изоляцию origin.
Handler отклоняет запрос, если `Host` не совпадает с `BaseURL`; reverse proxy
должен сохранить или восстановить этот canonical host перед dispatch. URL
является bearer credential. Без `BaseURL` и `SigningKey` обычный store
работает, а `TemporaryURL` возвращает `storage.ErrUnsupported`.
`MaxLinkTTL` и expiry отдельного вызова задаются целым числом секунд.

## Staged uploads и обслуживание

`Stage` пишет в приватную staging-зону и сохраняет expiry в header. `Promote`
атомарно помещает полный файл в final key. `Abort` и `Delete` идемпотентны.
`CleanupExpired` смотрит только валидные принадлежащие backend stage names и
удаляет не больше заданной порции; посторонние/повреждённые записи остаются для
оператора. Он также удаляет канонически названные same-root work-файлы после
crash процесса, но только когда они старше `storage.MaxStageTTL` (семи дней).
Свежие и иначе названные файлы не затрагиваются; такие residue входят в
`CleanupResult.Removed`.

TTL начинается в момент входа в `Stage`, а не после завершения долгой загрузки.
Задавайте его с запасом относительно максимальной длительности upload: если
загрузка пережила TTL, `Stage` может вернуть уже истёкший ID. Фоновый cleanup
backend не запускает — вызывайте его из scheduler приложения с context, у
которого есть deadline. `CleanupOptions.Limit` ограничивает число удалений, а не
просмотренных записей.
Для write- и maintenance-операций задавайте deadline короче
`storage.MaxStageTTL`: активный work-файл тогда не достигнет orphan-порога, а
зависшая filesystem-операция будет ограничена по времени.

## См. также

- [storage](storage.md) — общие операции, ошибки и lifecycle UI-upload
- [storageminio](storageminio.md) — тот же контракт поверх MinIO
