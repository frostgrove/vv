# authgin — аутентификация запроса Gin

```go
import "github.com/frostgrove/vv/auth/http/authgin"
```

**Модуль:** `github.com/frostgrove/vv/auth/http/authgin` — одна зависимость,
`github.com/gin-gonic/gin`
· **Зависит от:** `auth`, `authhttp`, `porthttp`, gin

Он **не** требует [crudgin](crudgin.md). Аутентифицировать запрос и отдавать
CRUD-ресурс — два решения, которые принимаются раздельно ([[D-051]]).

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
r := gin.New()
r.Use(crudgin.Errors(), authgin.Middleware(guard))
crudgin.New(articles).Mount(r, "/articles")
```

Шаг 1 — это [authjwt](authjwt.md), шаг 2 — [auth](auth.md), шаг 3 —
[security](security.md). Эта страница про шаг 4 и только про него.

или на одной группе:

```go
api := r.Group("/api", authgin.Middleware(guard))
```

## Что вы получаете

| | |
|---|---|
| `Middleware(guard, opts...)` | `gin.HandlerFunc` |

`opts` — это `porthttp.RenderOption`, те же, что принимает `crudgin.Errors`.

## Что он делает

Кладёт `auth.Principal` в контекст **`c.Request`**, а не в `c.Set`. Только его
видит репозиторий; принципал в собственном хранилище Gin невидим для любой
политики, и оба написания компилируются.

Отказ пишется здесь, а `c.Abort()` останавливает цепочку. Ошибка ещё и
подаётся через `c.Error`, чтобы собственный логирующий middleware Gin видел
причину, которой тело намеренно не несёт.

**Последовательная двойная установка с тем же guard аутентифицирует один раз** —
на движке и ещё раз на группе это обычный способ так сделать. Другой guard на
группе выполняет свою проверку. A -> B -> A отклоняется, потому что assurance
order неизвестен; cumulative checks монтируются один раз, альтернативы задаются
одним `auth.Chain` ([[D-076]]).

`Middleware` валидирует guard при построении; nil и `new(auth.Guard)` паникуют до
трафика.

## Чтение таблицы маршрутов для гейта

| | |
|---|---|
| `Routes(engine)` | что приложение на самом деле обслуживает, как `[]authhttp.Route` |
| `Verify(engine, declared, opts…)` | это же, сравнённое с декларациями, одним вызовом |

Читается собственная таблица Gin, а не список рядом: декларацию имеет смысл
сверять только со вторым утверждением, полученным независимо.

HEAD и OPTIONS не учитываются. Gin не генерирует HEAD так, как Fiber, поэтому
появившийся здесь был зарегистрирован намеренно — и всё равно пропускается,
чтобы три привязки не расходились в том, что декларация обязана покрывать
([[D-073]]).

## Смотрите также

- [auth](auth.md) · [authhttp](authhttp.md) · [crudgin](crudgin.md)
- [[D-051]] почему он не требует crudgin · [[UC-019]] · [[FL-019]] · [[FL-013]]
