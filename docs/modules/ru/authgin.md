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
| `Verify(engine, declared, opts…)` | это же, сравнённое с декларациями: относительными под префиксом и помеченными `authhttp.AtRoot` — для всего, что вне его |
| `VerifyAreas(engine, areas…)` | то же по всем смонтированным маршрутам — см. [authhttp](authhttp.md) |
| `AnswerPreflight(middleware, preflight)` | обёртка над middleware: CORS-preflight браузера отвечает названный вами обработчик, а не guard |
| `SkipPreflight(middleware)` | та же обёртка, когда отвечать некому: preflight получает `204` и не доходит ни до одного маршрута |

Читается собственная таблица Gin, а не список рядом: декларацию имеет смысл
сверять только со вторым утверждением, полученным независимо.

Не пропускается ничего. Gin не генерирует ни HEAD, ни OPTIONS, поэтому каждый
метод в этой таблице зарегистрирован намеренно и обязан объявить свой доступ —
включая написанный руками обработчик `OPTIONS`, который остаётся маршрутом,
как бы он ни походил на ответ CORS ([[D-073]]).

## Как пропустить CORS-preflight

Guard перед CORS-middleware отвечает на preflight браузера 401, и запрос, о
котором тот спрашивал, не случается вовсе.

```go
engine.Use(authgin.AnswerPreflight(authgin.Middleware(guard), cors.Default()))
```

Preflight — и только он: `OPTIONS`, `Origin`, `Access-Control-Request-Method` и
отсутствие заголовка `Authorization`. Такой запрос минует guard и уходит
названному обработчику, а после его возврата контекст прерывается — поэтому
**маршрут `OPTIONS`, который Gin позволяет написать руками, никогда не
выполняется неаутентифицированным** ([[D-103]]). `OPTIONS` с credential проходит
аутентификацию как любой другой запрос.

`SkipPreflight(middleware)` — та же обёртка, когда отвечать некому: она сама
отдаёт `204` без заголовков `Access-Control-Allow-*` — видимая ошибка CORS вместо
открытой двери, ведь оба заголовка, которые читает предикат, ставит клиент.

## Смотрите также

- [auth](auth.md) · [authhttp](authhttp.md) · [crudgin](crudgin.md)
- [[D-051]] почему он не требует crudgin · [[UC-019]] · [[FL-019]] · [[FL-013]]
