# authnet — аутентификация запроса net/http

```go
import "github.com/frostgrove/vv/auth/http/authnet"
```

**Модуль:** корневой — импортирует только стандартную библиотеку, поэтому ничего
не стоит · **Зависит от:** `auth`, `authhttp`, `porthttp`, `net/http`

---

## Подключение

Четыре шага, и третий — тот, который проще всего забыть.

**1. Чем проверяются учётные данные.** Здесь — вашим собственным
HMAC-секретом. `authjwt.RSA` и `authjwt.JWKS` нужны для токенов, которые
выпускает кто-то другой, а [apikey](apikey.md) — вообще другой провайдер.

```go
authn := authjwt.Standard(
	authjwt.HMAC([]byte(os.Getenv("JWT_SECRET"))),
	auth.RoleMap{"editor": {"article:read", "article:write"}},
	authjwt.Issuer("https://id.example.com"),
	authjwt.Audience("articles-api"),
)
```

**2. Guard** — то, что читает запрос и кладёт вызывающего в контекст. Какой
заголовок читать, обязательны ли учётные данные вообще и как не проверять один
запрос дважды — всё здесь.

```go
guard := auth.NewGuard(authn)
```

**3. Что этому вызывающему позволено.** Пропустите этот шаг — и middleware
будет только аутентифицировать: принципал, лежащий в контексте, сам по себе не
сужает ни одного запроса.

```go
policy := security.Combine(
	security.RequirePermission[Article, int64]("article:read"),
	security.ScopeAttr[Article, int64]("TenantID", "tenant"),
)
articles := Articles.Bind(db, security.Gate(policy))
```

**4. Подключение.**

```go
mux := http.NewServeMux()
crudnet.New(articles).Mount(mux, "/articles")

http.ListenAndServe(":8080", crudnet.Errors()(authnet.Middleware(guard)(mux)))
```

Шаг 1 — это [authjwt](authjwt.md), шаг 2 — [auth](auth.md), шаг 3 —
[security](security.md). Эта страница про шаг 4 и только про него.

`authnet.Handler(guard, next)` — то же самое для одного маршрута, когда соседние
не аутентифицируются.

## Что вы получаете

| | |
|---|---|
| `Middleware(guard, opts...)` | обычный `func(http.Handler) http.Handler` |
| `Handler(guard, next, opts...)` | то же, применённое к одному обработчику |

`opts` — это `porthttp.RenderOption`, те же, что принимает `crudnet.Errors`, так
что отказ рендерится через ваш словарь кодов и ваш каталог сообщений.

## Что он делает

Читает учётные данные, проверяет их и кладёт `auth.Principal` в `r.Context()`.
Это единственный канал, доходящий до репозитория: транспортный хук может
отклонить запрос, но не может переписать контекст, который увидит репозиторий.

Отказ пишется здесь, и следующий обработчик не запускается. Он рендерится тем же
конвертом, что и любая другая ошибка, поэтому клиент видит одну форму ошибки —
отказали ему на двери или в репозитории.

**Двойная установка аутентифицирует один раз.** Контекст, где принципал уже
есть, возвращается нетронутым.

`Middleware(nil)` паникует. Middleware без guard'а ничего не аутентифицирует,
выглядя при этом ровно как тот, который аутентифицирует.

## Не только ServeMux

`http.ServeMux` — то, что в примере; сам middleware это простой
`func(http.Handler) http.Handler`, поэтому chi, gorilla/mux и httprouter
принимают его напрямую.

## Смотрите также

- [auth](auth.md) — guard, опции и всё транспортно-нейтральное
- [authhttp](authhttp.md) — где пишется отказ
- [authgin](authgin.md) · [authfiber](authfiber.md) · [authgrpc](authgrpc.md)
- [[UC-019]] · [[FL-019]] · [[FL-013]]
