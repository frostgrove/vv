# authfiber — аутентификация запроса Fiber v3

```go
import "github.com/frostgrove/vv/auth/http/authfiber"
```

**Модуль:** `github.com/frostgrove/vv/auth/http/authfiber` — одна зависимость,
`github.com/gofiber/fiber/v3`
· **Зависит от:** `auth`, `authhttp`, `porthttp`, fiber v3

Он **не** требует [crudfiber](crudfiber.md) ([[D-051]]).

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
app := fiber.New(fiber.Config{ErrorHandler: crudfiber.ErrorHandler()})
app.Use(authfiber.Middleware(guard))
app.Use("/articles", crudfiber.New(articles).Routes())
```

Шаг 1 — это [authjwt](authjwt.md), шаг 2 — [auth](auth.md), шаг 3 —
[security](security.md). Эта страница про шаг 4 и только про него.

или на одной группе:

```go
api := app.Group("/api", authfiber.Middleware(guard))
```

## Что вы получаете

| | |
|---|---|
| `Middleware(guard, opts...)` | `fiber.Handler` |

## Единственное, что здесь иначе

**Принципал кладётся в контекст через `c.SetContext`, а не в `Locals`.**

`Locals` — обычное место, куда middleware Fiber кладёт состояние запроса, и для
этого случая оно неверное: `crudfiber` передаёт вниз, в слой `port`, именно
`c.Context()`, поэтому принципал в `Locals` невидим для любой политики. Оба
написания компилируются, оба выглядят правильно на ревью, и только одно из них
сужает запрос.

`TestAnAuthenticatedRequestReachesTheHandlerWithItsPrincipal` в
`auth/http/authfiber/middleware_test.go` — то, что падает, когда это сделано не так.

## Всё остальное

Отказ пишется здесь, а не возвращается — по причине, изложенной в
[authhttp](authhttp.md). `crudfiber.Errors` и `crudfiber.ErrorHandler` оставляют
уже записанный ответ в покое, так что это композируется с любым из них.

**Двойная установка аутентифицирует один раз.** `Middleware(nil)` паникует.

## Чтение таблицы маршрутов для гейта

| | |
|---|---|
| `Routes(app)` | что приложение на самом деле обслуживает, как `[]authhttp.Route` |
| `Verify(app, declared, opts…)` | это же, сравнённое с декларациями, одним вызовом |

Читается собственная таблица Fiber, а не список, который ведут рядом. В этом весь
смысл: декларацию имеет смысл сверять только со вторым утверждением, полученным
независимо, а запись при регистрации совпадала бы с декларацией ровно тогда,
когда обе неверны.

HEAD и OPTIONS не учитываются. Fiber сам регистрирует HEAD для каждого GET — после
того как отработает его стартовая процедура, — а флаг «сгенерирован»
неэкспортируемый, так что снаружи сгенерированный HEAD и написанный руками —
одно и то же значение. Поэтому маршрут только на HEAD здесь объявить нельзя — по
той же причине, по которой его нельзя смонтировать отдельно. OPTIONS — это ответ
CORS-middleware, а не эндпоинт ([[D-073]]).

## Смотрите также

- [auth](auth.md) · [authhttp](authhttp.md) · [crudfiber](crudfiber.md)
- [[UC-019]] · [[FL-019]] · [[FL-013]]
