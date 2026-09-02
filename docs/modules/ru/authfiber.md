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

**Повторяющийся заголовок отказа сохраняет все значения.** Отрисованные
заголовки пишутся через `c.Response().Header.Add`; `Ctx.Set` перезаписывает,
поэтому 401 с двумя вызовами `WWW-Authenticate` уходил с последним из них и
выглядел нормально. `TestARefusalCarriesEveryHeaderTheRendererAskedFor` в
`auth/http/authfiber/refuse_test.go` — то, что падает, когда это сделано не так;
то же имя теста стоит над response writer'ами двух других биндингов.

**Последовательная двойная установка с тем же guard аутентифицирует один раз.**
Другой guard выполняет свою проверку. A -> B -> A отклоняется, потому что
assurance order неизвестен; cumulative checks монтируются один раз,
альтернативы задаются одним `auth.Chain` ([[D-076]]). `Middleware` валидирует
guard при построении; nil и `new(auth.Guard)` паникуют до трафика.

## Чтение таблицы маршрутов для гейта

| | |
|---|---|
| `Routes(app)` | что приложение на самом деле обслуживает, как `[]authhttp.Route` |
| `Verify(app, declared, opts…)` | это же, сравнённое с декларациями: относительными под префиксом и помеченными `authhttp.AtRoot` — для всего, что вне его |
| `VerifyAreas(app, areas…)` | то же по всем смонтированным маршрутам — см. [authhttp](authhttp.md) |
| `AnswerPreflight(handler, preflight)` | обёртка над middleware: CORS-preflight браузера отвечает названный вами обработчик, а не guard |
| `SkipPreflight(handler)` | та же обёртка, когда отвечать некому: preflight получает `204` и не доходит ни до одного маршрута |

Читается собственная таблица Fiber, а не список, который ведут рядом. В этом весь
смысл: декларацию имеет смысл сверять только со вторым утверждением, полученным
независимо, а запись при регистрации совпадала бы с декларацией ровно тогда,
когда обе неверны.

Не учитывается тот HEAD, который придумал Fiber; всё, что смонтировал потребитель,
учитывается. Fiber регистрирует HEAD для каждого GET после своей стартовой
процедуры, а флаг «сгенерирован» неэкспортируемый — поэтому сгенерированная
половина узнаётся по форме: HEAD, на чьём пути есть ещё и GET. HEAD без GET рядом
и обработчик OPTIONS — такая же поверхность, как любая другая, и обязаны объявить
свой доступ. Что эта форма различить не может — написанный руками HEAD на пути,
который обслуживает и GET; он покрыт GET-декларацией этого пути, и это принятое
ограничение ([[D-073]]).

## Как пропустить CORS-preflight

Перед кросс-доменной записью браузер отправляет `OPTIONS` без credential. Guard,
смонтированный перед CORS-middleware, отвечает на него 401, браузер так и не
делает запрос, о котором спрашивал, а выглядит это как неверная настройка CORS.

```go
app.Use(authfiber.AnswerPreflight(authfiber.Middleware(guard), cors.New()))
```

Preflight — это `OPTIONS`, `Origin`, `Access-Control-Request-Method` и отсутствие
заголовка `Authorization`, и ничего больше. Подошедший запрос минует guard и
уходит названному обработчику, который на него и отвечает; **цепочка на этом
заканчивается, ни один маршрут не выполняется** ([[D-103]]). `OPTIONS` с
credential — это запрос, и он проходит аутентификацию как любой другой.

`SkipPreflight(handler)` — та же обёртка, когда отвечать некому: она сама отдаёт
`204` без заголовков `Access-Control-Allow-*`. Это видимая ошибка CORS в браузере
вместо неаутентифицированного `OPTIONS`, выполнившего написанный руками
обработчик: оба заголовка, которые читает предикат, ставит клиент, поэтому всё
достижимое за ними достижимо для кого угодно.

Поставить CORS-middleware перед guard — то же самое и лучший ответ там, где
цепочку выстраиваете вы; это — для цепочки, которую выстраиваете не вы.

## Смотрите также

- [auth](auth.md) · [authhttp](authhttp.md) · [crudfiber](crudfiber.md)
- [[UC-019]] · [[FL-019]] · [[FL-013]]
