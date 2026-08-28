# storageminio

`github.com/frostgrove/vv/storage/storageminio` реализует `storage.Backend` через
`minio-go/v7`. Это отдельный Go module, поэтому filesystem-only приложение не
получает SDK и его зависимости.

## Установка и создание

```bash
go get github.com/frostgrove/vv/storage/storageminio
```

MinIO client создаёт приложение: endpoint, credentials, TLS, transport и retry
policy принадлежат bootstrap-коду. Затем client передаётся адаптеру:

```go
backend, err := storageminio.New(storageminio.Config{
    Client:     minioClient,
    Bucket:     "app-files",
    Prefix:     "production", // optional, проверяется и ограничен
    MaxLinkTTL: time.Hour,
})

avatars, err := storage.New(storage.Config{
    Namespace: "avatars",
    Backend:   backend,
})
```

Адаптер не читает credentials из environment, не создаёт client, не ходит в
сеть при construction и не управляет shutdown client. В logical key нет
endpoint, bucket или физического prefix.

## Семантика write и promotion

`CreateOnly` отправляет `If-None-Match: *`: существующий объект становится
`storage.ErrAlreadyExists`, а конкурентный conditional conflict остаётся
`storage.ErrConflict`. Для него всегда используется один conditional PUT, а
максимальный размер равен `storageminio.MaxCreateOnlySize` (5 GiB). Больший
`CreateOnly` с объявленным размером возвращает `storage.ErrUnsupported` до
чтения source; source неизвестной длины можно отклонить только после получения
размера приватного stage. Это ограничение обходит особенность `minio-go/v7`:
SDK не сохраняет custom conditional headers при завершении multipart upload.
`Replace` безусловен только при явном выборе и может использовать штатный
multipart SDK.

`CreateOnly` source неизвестной длины сначала стримится в случайный приватный
stage, где может использоваться штатный multipart SDK. Адаптер сразу открывает
stage, получает точный размер и стримит его в финальный conditional single PUT.
При объявленном размере checking reader обнаруживает early EOF и байт N+1 до
успешной видимости; нулевой размер проверяется до вызова SDK. Source вызывающего
кода не закрывается, uncertain final write автоматически не повторяется.

Stages живут в приватном prefix с reserved marker/expiry metadata. Promotion
стримит stage в conditional final put, затем best-effort удаляет stage. Это
намеренно `Promote`, не atomic rename: final PUT с последующим удалением stage
не равен filesystem rename. Коллизия destination сохраняет stage; cleanup
residue удаляется после TTL через `CleanupExpired`.

Приватный conditional claim выбирает ровно одну активную операцию
`Promote`/`Abort` для каждого StageID, в том числе при конкурентных promotions
в разные final keys. Проигравший получает `storage.ErrConflict`. Каждый переход
состояния пишет новый случайный token в body с точным `If-Match` по ETag. Если
переход committed, но ответ потерян, старый retry SDK поэтому не может
перезаписать следующего владельца claim.

После deterministic failure остаётся переиспользуемый `retired` claim, пока
stage существует. Когда отсутствие stage подтверждено, адаптер CAS-переходом
делает claim невозобновляемым `terminal`, а затем удаляет его. Захват проверяет
наличие stage непосредственно до и после conditional write; сама операция тоже
проверяет stage после захвата. Поэтому даже задержанный create или повторный
terminal delete не может повторно Promote уже использованный StageID. Остатки
terminal cleanup повторно обрабатывает `CleanupExpired`; claims не накапливаются
на каждый завершённый upload. Нельзя вручную удалять active/retired claims или
применять lifecycle expiration к completed claim prefix: это обойдёт fencing
protocol.

Если deterministic переход claim не удался, наружу возвращается ошибка cleanup,
а не ложное обещание немедленного retry. При uncertain final result, а также
при committed final object с неудалённым stage, active generation хранится до
ограниченного expiry и не позволяет положить один stage во второй key.

Claim истекает не позже `storage.MaxStageTTL` (семи дней). Для `Promote`,
`Abort` и cleanup задавайте более короткие context deadlines: гарантия одной
активной операции рассчитана на запрос, который завершится до safety-expiry, а
не останется работать несколько дней.

Сервер обязан строго выполнять conditional single PUT для missing object,
конкурентных writes и ошибок read quorum. Для MinIO рекомендуется build,
содержащий [исправление conditional writes #21653](https://github.com/minio/minio/pull/21653)
(`18f97e7`, 2025-10-24), следующее за исправлением missing object
[#21550](https://github.com/minio/minio/pull/21550), либо эквивалентный build,
проходящий эти conformance cases. Одна версия SDK не обеспечивает эту серверную
гарантию.

Для bucket нужно настроить lifecycle rule, удаляющий незавершённые multipart
uploads. SDK пытается abort-ить свою multipart session после ошибки source, но
при cancellation/deadline на сервере может остаться незавершённый upload.
`CleanupExpired` его не видит, потому что это ещё не завершённый object.

## Reads, metadata и ссылки

`Open` использует немедленный GET, поэтому missing/forbidden возвращаются самим
вызовом, а не первым чтением body. Body принадлежит caller. В `storage.Info`
попадает только ограниченная portable user metadata; deferred-ошибки read/close
преобразуются в переносимые скрывающие детали storage errors. Reserved staging
metadata и сырые SDK headers остаются в adapter.

`TemporaryURL` использует штатный pre-signed GET MinIO и общие TTL-ограничения.
TTL задаётся целым числом секунд в соответствии с точностью SigV4, а сообщаемый
expiry консервативен. Возвращаемый `storage.Link` чувствителен и скрывает обычное
форматирование.

## Проверка с сервером

Обычные unit/wire tests используют injected SDK seam и не требуют сети.
Deployment, заявляющий MinIO compatibility, должен дополнительно прогнать
integration scenarios на своей точной версии сервера — особенно конкурентный
conditional single PUT, multipart staging/replace и pre-signed GET. Проверка
должна включать conditional PUT с неверным ETag и ошибкой read quorum. Lifecycle
rule для incomplete multipart входит в deployment checks, но не должен удалять
completed claim objects.

## См. также

- [storage](storage.md) — общие операции, ошибки и lifecycle UI-upload
- [storagefs](storagefs.md) — stdlib filesystem backend и HMAC link handler
