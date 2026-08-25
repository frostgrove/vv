# query — DSL для передачи по проводу

```go
import "github.com/shardit-io/vv/crud/query"
```

**Модуль:** корневой · **Зависит от:** `crud` и стандартной библиотеки
· **Contract manifest:** есть ([[D-048]])

Один JSON-документ компилируется в `crud.Options`. Каждый путь резолвится
относительно модели **до того, как появится хоть какой-то SQL**, поэтому
неизвестное поле — это отказ с указанием проблемного пути, а не тихо
отброшенное условие ([[D-013]]).

**Вы почти никогда не импортируете этот пакет напрямую** — четыре транспортных
биндинга говорят на нём за вас. Импортируйте его, чтобы ограничить, что вправе
запросить клиент (`query.Config`), или чтобы принимать документ запроса на
своём собственном эндпоинте.

---

## Документ

```json
{
  "page": 2, "limit": 20,
  "sort": ["-createdAt", "author.name"],
  "preload": ["author", {"path": "comments", "filter": {"approved": true}}],
  "select": ["id", "title"],
  "search": "generics", "searchFields": ["title", "body"],
  "filter": {
    "views":       {"gte": 100, "lt": 1000},
    "status":      ["draft", "review"],
    "author.name": {"contains": "an"},
    "tags.slug":   {"in": ["go", "rust"]},
    "publishedAt": {"isNull": false},
    "or":  [ {"title": "a"}, {"and": [{"views": {"gt": 10}}, {"pinned": true}]} ],
    "not": {"title": "spam"}
  }
}
```

| Ключ | Значение |
|---|---|
| `page`, `limit`, `offset` | постраничная выборка по смещению |
| `after`, `before` | постраничная выборка по курсору — непрозрачная строка, отданная предыдущей страницей. Заменяет `page`/`offset` |
| `sort` | `["-views", "author.name"]`. Ведущий `-` — по убыванию |
| `select` | проекция. Колонки join'ов из preload сохраняются автоматически |
| `preload` | `["author"]` или `[{"path": "comments", "filter": {…}}]` |
| `filter` | вложенная форма, см. ниже |
| `terms` | плоская форма, соединяется с `filter` через AND |
| `search`, `searchFields` | заключённое в скобки OR по текстовым колонкам |
| `unpaged` | запросить всё — всё равно ограничено `MaxLimit` репозитория |
| `skipTotal` | отбросить `COUNT` |
| `distinct` | `SELECT DISTINCT` |

## Операторы

```
eq  ne  gt  gte  lt  lte
like  notLike  ilike  contains  startsWith  endsWith
in  notIn  between  isNull  isNotNull
```

С алиасами через `$`-префикс и символьными — `$gte`, `>=`. Голое значение
означает `eq`, голый массив — `in`, `null` — `IS NULL`.

Имена полей сопоставляются без учёта регистра и разделителей, поэтому
`createdAt`, `created_at` и `CreatedAt` — одна и та же колонка.

## Три вещи, которые здесь сделаны правильно

- **Значения типизируются по своей колонке.** `{"views": {"gte": 100}}`
  биндится как `int`, а не `float64`; `{"createdAt": {"gte": "2026-01-02"}}`
  биндится как `time.Time`. Колонка, чей тип реализует `TextUnmarshaler`,
  парсится через него, так что типы uuid и enum сохраняют собственные правила
  ([[FL-012]]).
- **Search не может вырваться за пределы своего скоупа.** OR по полям поиска —
  отдельный узел AST, поэтому он всегда в скобках. Конкатенация
  `a LIKE ? OR b LIKE ?` прямо в `WHERE` — вот как отфильтрованный список
  тихо начинает возвращать всё подряд.
- **Вывод детерминирован.** У JSON-объектов нет порядка, а у Go-карт его ещё
  меньше; ключи сортируются перед компиляцией, поэтому один и тот же запрос
  всегда даёт один и тот же оператор — и его можно тестировать ([[D-014]]).

## Форма в строке запроса

Та же семантика, для `GET`:

```
?page=2&limit=20&sort=-createdAt,author.name&preload=author,comments.author
&select=id,title&q=generics&searchFields=title,body
&f=views:gte:100&f=tags.slug:in:go,rust&f=publishedAt:isNull:true
&filter={"or":[{"status":"draft"}]}
```

`f=field:op:value` повторяется и соединяется через AND. **Структурны только
первые два двоеточия**, поэтому таймстампы не ломаются. `filter=` принимает
полный JSON-документ для всего, что плоская форма выразить не может.

---

## Ограничение

DSL управляется недоверенным вводом, поэтому ограничивайте его на каждом
эндпоинте ([[UC-002]]).

```go
cfg := &query.Config{
    Filterable:  []string{"Title", "Views", "Author.*"},  // .* разрешает поддерево
    Sortable:    []string{"CreatedAt", "Views"},
    Selectable:  []string{"ID", "Title", "Views"},
    Preloadable: []string{"Author", "Comments", "Comments.Author"},
    Searchable:  []string{"Title", "Body"},

    DefaultSearchFields: []string{"Title"},

    MaxDepth:      4,   // вложенность and/or и длина пути
    MaxConditions: 32,  // листовых сравнений в одном документе
    MaxPreloads:   4,
}
```

**Пустые списки разрешают всё, что маппит модель.** Ограничения на глубину,
условия и preload действуют в любом случае. Нулевой `Config` — рабочее
значение по умолчанию; ужесточайте его там, где эндпоинт публичный.

Подключите его к любому биндингу:

```go
crudfiber.WithQuery[Article, int64, ArticleUpdate](cfg)
crudgin.WithQuery[…](cfg)
crudnet.WithQuery[…](cfg)
crudgrpc.WithQuery[…](cfg)
port.WithQuery(cfg)
```

## Использование напрямую

```go
var req query.Request
if err := json.NewDecoder(r.Body).Decode(&req); err != nil { … }

opts, err := req.Compile(articles.Meta(), cfg)
if err != nil {
    var qe *query.Error
    if errors.As(err, &qe) {
        // qe.Path и qe.Reason оба безопасно отдавать наружу
    }
}
page, err := articles.Get(ctx, opts...)
```

`query.ParseQuery(url.Values)` парсит форму строки запроса. `query.Coerce(s, t)`
конвертирует одно текстовое значение в Go-тип через собственный
`TextUnmarshaler` этого типа — экспортирована, потому что транспортам она
нужна и для параметров пути.

**Всё, что лежит в `query.Error`, безопасно рендерить.** Он называет путь,
который был неверен, и почему — и никогда не называет внутреннее имя
([[D-044]]).

## См. также

- [port](port.md) — где скомпилированный запрос становится командой
- [crudhttp](crudhttp.md) — как `query.Error` становится 400
- [[UC-002]] позволить недоверенному клиенту запрашивать данные · [[UC-006]] запрос и сортировка по связям
- [[FL-012]] значение с провода становится значением Go
