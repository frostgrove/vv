# crudhttp — то, что одновременно HTTP *и* CRUD

```go
import "github.com/shardit-io/vv/crud/http/crudhttp"
```

**Модуль:** корневой · **Зависит от:** `crud`, `query`, `errs`, `port`, `port/porthttp`, `net/http`

Остаток после двух разрезов: формы запроса, которые есть у CRUD-маршрута и
больше ни у кого, плюс слой совместимости поверх всего, что уехало.

**Скорее всего, импортировать не нужно.** Если вы монтируете маршруты,
[crudnet](crudnet.md), [crudfiber](crudfiber.md) и [crudgin](crudgin.md)
реэкспортируют нужное. Если вы рендерите собственные тела ответов или
устанавливаете middleware ошибок, вам нужна страница [porthttp](porthttp.md).

---

## Куда что уехало

| Что | Куда | Почему |
|---|---|---|
| команды, `Service`, `Mapper`, словарь кодов, `port.Violations` | [port](port.md) | ничто из этого не HTTP ([[D-045]]) |
| таблица статусов, конверт, шов `Renderer`, `DecodeJSON`, локаль, запасной вариант по сырому телу | [porthttp](porthttp.md) | всё это HTTP и ничто из этого не CRUD ([[D-059]]) |
| клиентский транспорт | [remotehttp](remotehttp.md) | это клиент, а не сервер ([[D-058]]) |
| то, что на этой странице | здесь | это HTTP *и* CRUD |

Два теста, именно в таком порядке:

1. **Сможет ли не-HTTP-транспорт это реализовать, не импортируя `net/http`?**
   Если нет — это не `port`. `Renderer`, возвращающий `http.Header`, не сможет;
   поэтому шов не в `errs`.
2. **Сможет ли не-CRUD-подсистема это взять, не импортируя `crud`?**
   Если да — это `porthttp`. Auth-middleware отвечает 401 через ту же таблицу
   статусов и тот же конверт.

## Что здесь на самом деле

| | |
|---|---|
| `Repository[M, ID, U]` | generic-алиас `port.Repository` — интерфейс, который принимает каждый биндинг ([[D-022]]) |
| `BulkDeleteRequest[ID]` | тело `{"ids":[…]}` для `POST /bulk-delete` |
| `CoerceID[ID](raw string)` | параметр пути становится типом ключа — именно поэтому uuid или slug работают в URL без единой строки кода |
| `NarrowForCount(*query.Request)` | отбросить всё, что ничего не значит для `COUNT` |
| `NarrowForEntity(*query.Request)` | оставить только опции формы ответа |
| `Sanitize` · `ClearGenerated` | что клиент не может выбирать при создании: сгенерированный ключ, колонка `generated` |
| `Rules` | пять настроек, которые ничего не говорят о транспорте, общие для всех четырёх биндингов — алиас [`port.Rules`](port.md#правила-которые-биндингу-не-принадлежат) |

`CoerceID` и далее до `Sanitize` · `ClearGenerated` — форвардеры поверх
[port](port.md); они экспортируются здесь, потому что их вызывает приложение,
которое пишет собственный маршрут создания ([[D-045]]). `Rules` в их число не
входит — это алиас типа, который здесь никогда не жил.

## Форвардеры

`crud/http/crudhttp/porthttp.go` реэкспортирует всё, что перенёс [[D-059]]:
`Renderer`, `EnvelopeRenderer`, `RenderOption`, `Envelope`, `Groups`,
`MaxViolations`, `DefaultRetryAfter`, `MaxKeptBody`, `MaxBody`, `ErrBadRequest`,
`NewRenderer`, пять опций `With…`, `Internal`, `Status`, `StatusFor`,
`KindForStatus`, `KindOf`, `ParseEnvelope`, `BadRequest`, `BadRequestf`,
`BadRequestAs`, `MalformedBody`, `TooLarge`, `BodyResolver`, `DecodeJSON`,
`DecodeJSONKeep`, `DecodeJSONKeepLimit`,
`KeepBody`, `WithBody`, `BodyFrom`, `WithLocale`, `LocaleFrom` и
`AcceptLanguage`.

Это алиасы и однострочные вызовы без поведения, и файл говорит об этом сверху:
символ, у которого там появилось тело, перестал быть форвардером и принадлежит
одной из сторон разреза. Перенаправление алиаса — не ломающее изменение; на этом
же приёме въехал [[D-034]].

Новый код должен импортировать [porthttp](porthttp.md) напрямую.

## См. также

- [porthttp](porthttp.md) — таблица статусов, конверт, рендерер
- [port](port.md) — транспортно-нейтральная половина
- [remotehttp](remotehttp.md) — клиентская сторона
- [crudnet](crudnet.md) · [crudfiber](crudfiber.md) · [crudgin](crudgin.md) — оболочки поверх этого
- [[FL-013]] запрос через другой биндинг · [[FL-015]] запрос через слой port
- [[D-059]] HTTP-проекция принадлежит `port` · [[D-045]] общая половина транспортно-нейтральна
