# authgrpc — аутентификация вызова gRPC

```go
import "github.com/shardit-io/vv/rpc/authgrpc"
```

**Модуль:** `github.com/shardit-io/vv/rpc/authgrpc` — одна зависимость,
`google.golang.org/grpc`
· **Зависит от:** `auth`, grpc

Он **не** требует [crudgrpc](crudgrpc.md) ([[D-051]]).

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
srv := grpc.NewServer(
	grpc.ChainUnaryInterceptor(crudgrpc.Errors(), authgrpc.Unary(guard)),
	grpc.ChainStreamInterceptor(authgrpc.Stream(guard)),
)
crudgrpc.New(articles).Register(srv, "Article")
```

Шаг 1 — это [authjwt](authjwt.md), шаг 2 — [auth](auth.md), шаг 3 —
[security](security.md). Эта страница про шаг 4 и только про него.

## Что вы получаете

| | |
|---|---|
| `Unary(guard, opts...)` | `grpc.UnaryServerInterceptor` |
| `Stream(guard, opts...)` | `grpc.StreamServerInterceptor` |
| `Skip(fullMethods...)` | оставить названные методы без аутентификации |

## Чем это отличается от трёх HTTP-привязок

**Учётные данные приходят из метаданных, а не из заголовка.** Ключи — те же
имена в нижнем регистре, а `metadata.MD.Get` регистронезависим, поэтому
`Authorization` по умолчанию находит то, что клиент прислал как `authorization`.

**Таблицы статусов здесь нет.** Отказ — это ошибка, возвращённая
интерсептором; рендерит её `crudgrpc.Errors`, а `errs.KindUnauthorized` уже
отображается в `UNAUTHENTICATED`. Так что этот пакет не пишет статус, и порядок
из [[D-008]] не затронут.

**Здесь нет 404, которому можно проиграть.** Строка, скрытая сужением,
по-прежнему `NOT_FOUND` дальше по цепочке — это ответ репозитория, не этого
пакета.

## Skip

```go
authgrpc.Unary(guard, authgrpc.Skip(
	"/grpc.health.v1.Health/Check",
	"/vv.crud.v1.Article/List",
))
```

Имя — полное, с ведущим слешем, как оно выглядит в
`grpc.UnaryServerInfo.FullMethod`. **Префикс не принимается**: точный список
проверяем глазами, а префикс молча расширится в день, когда кто-то добавит под
ним метод.

Ради этого и придуман `crudgrpc.ServicePrefix`. У каждого ресурса своё имя
сервиса, поэтому `/vv.crud.v1.Article/Create` и `/vv.crud.v1.Comment/Create` —
разные методы, которые правило может различить; под общим сервисом это был бы
один метод.

## Два ограничения, названные, а не оставленные на догадку

**Стрим аутентифицируется один раз, при открытии.** Истечение учётных данных
посреди стрима не замечается — интерсептор отрабатывает до первого сообщения и
больше не вызывается. Долгоживущий стрим, которому нужна перепроверка, делает её
в своём цикле.

**Сертификат пира не читается.** mTLS — другая аутентификация, и её принципал
приходит из `credentials.AuthInfo`; напишите её как `auth.Authenticator` и
передайте в тот же guard.

## Смотрите также

- [auth](auth.md) · [crudgrpc](crudgrpc.md)
- [[D-055]] · [[D-056]] · [[UC-019]] · [[FL-019]] · [[FL-013]]
