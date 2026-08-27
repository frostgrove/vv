# specs — спецификации и Criteria API

```go
import "github.com/frostgrove/vv/crud/decorators/specs"
```

**Модуль:** корневой · **Зависит от:** `crud` и стандартной библиотеки

`Specification<T>` и `CriteriaBuilder` из JPA, плюс генерируемая метамодель,
которую проверяет компилятор. Собирайте переиспользуемые фрагменты запроса,
называйте их и передавайте как обычные значения.

**Берите его, когда** один и тот же фильтр встречается в трёх обработчиках,
когда запрос собирается из частей, которые выбирает вызывающий код, или когда
вы хотите, чтобы переименованная колонка ломала сборку, а не запрос.

---

## Соответствие

| JPA | vv |
|---|---|
| `Specification<T>` | `specs.Specification[M]` |
| `Root<T>`, `CriteriaBuilder` | `specs.Root[M]`, `specs.Builder` |
| `Specification.where(a).and(b).or(c).not()` | `specs.Where(a).And(b).Or(c).Not()` |
| `JpaSpecificationExecutor<T>` | `specs.Executor(repo)` |
| сгенерированная метамодель `User_` | `specs.Metamodel[User, userAttrs]()` |

---

## Два способа написать спецификацию

### Буквальная форма

```go
func IsActive() specs.Specification[User] {
    return specs.Of[User](func(root specs.Root[User], cb specs.Builder) crud.Predicate {
        return cb.Equal(root.Get("Active"), true)
    })
}

adults := specs.Where(IsActive()).And(specs.Of[User](
    func(r specs.Root[User], cb specs.Builder) crud.Predicate {
        return cb.GreaterThanOrEqualTo(r.Get("Age"), 18)
    }))
```

`Root.Get` принимает имя поля Go — имя колонки тоже подойдёт — и может
пересекать связь: `root.Get("Author.Name")`.

### Форма с метамоделью

Тот же результат, но проверенный компилятором. [`cmd/vv`](vv-cli.md) сам
пишет структуру атрибутов за вас.

```go
type userAttrs struct {
    ID        specs.Ord[User, int64]
    Email     specs.Str[User]
    Age       specs.Ord[User, int]
    Active    specs.Attr[User, bool]
    CreatedAt specs.Cmp[User, time.Time]
}

var User_ = specs.Metamodel[User, userAttrs]()   // проверяется при инициализации пакета

adults := specs.Where(User_.Active.Eq(true)).And(User_.Age.Gte(18))
```

Переименованная колонка падает при инициализации, называя поле. Значение
неверного типа не пройдёт компиляцию.

Сгенерированная метамодель раскрывается и через связи, до `-depth` (по
умолчанию 2), и никогда не возвращается в модель, уже стоящую на пути:

```go
Article_.Views.Gte(100)              // "views" >= $1
Article_.Author.Name.Eq("Ann")       // EXISTS (… authors … name = $1)
Article_.Comments.Approved.Eq(true)  // EXISTS (… comments … approved = $1)
Article_.Author.Name.Desc()          // ORDER BY (SELECT … LIMIT 1) DESC
```

---

## Типы атрибутов

Выбирайте самый узкий — от него зависит набор доступных методов.

| Тип | Конструктор | Добавляет |
|---|---|---|
| `Attr[M, T]` | `Attribute[M, T](field)` | `Eq` `Ne` `In` `NotIn` `IsNull` `NotNull` `Asc` `Desc` |
| `Cmp[M, T]` | `Comparable[M, T](field)` | то же самое плюс `Gt` `Gte` `Lt` `Lte` `Between` для типов вне `cmp.Ordered`, вроде `time.Time` |
| `Ord[M, T]` | `Ordered[M, T](field)` | то же самое, для `cmp.Ordered` |
| `Str[M]` | `Text[M](field)` | то же самое плюс `Like` `NotLike` `LikeIgnoreCase` и безопасные для литерала `Contains` / `StartsWith` / `EndsWith` с вариантами `IgnoreCase` |

Каждый метод возвращает `Specification[M]`, так что они composable с `Where`,
`AllOf`, `AnyOf` и `Not`.

`Name()` у любого атрибута отвечает каноническим именем поля модели, к которому
он привязался. Именно им адресуются настройки и опции, принимающие *имя*, а не
предикат:

```go
sqlrepo.SoftDelete(Doc_.DeletedAt.Name())
crud.GroupBy(Order_.Status.Name())
crud.Sum("total", Order_.Amount.Name())
security.Freeze[Doc, int64](Doc_.TenantID.Name())
```

## Хэндлы связей

Сгенерированная группа связи несёт собственный путь, поэтому и связь
адресуется идентификатором:

```go
Article_.Comments.Path()          // "Comments"
Article_.Comments.Author.Path()   // "Comments.Author"
```

Именно это принимают `sqlrepo.RelationScope`, `crud.Preload`, `crud.PreloadWhere`
и `security.ScopeRelationField` вместо литерала. Хэндл также помнит модель, на
которую путь приземляется, поэтому хэндл, указывающий на не ту модель, падает
при инициализации пакета, а не сужает не ту таблицу.

`Path`, `RelPath` и `String` отвечают одной и той же строкой, и три формы — из-за
встраивания. Хэндл встроен в группу, поэтому `Path` продвинут на уровень наружу,
а каждая колонка *целевой* модели — поле той же группы на уровень ближе, и Go
разрешает ближнее. Колонка `Path` у цели, таким образом, затеняет метод, и
`Folder_.Files.Path()` перестаёт компилироваться для этой одной связи.
Сгенерированный файл говорит об этом в doc-комментарии группы; `RelPath()` —
форма, которую ничто не затеняет.

Группа существует только там, где генератор раскрыл связь, а этим управляет
`-depth`; связь, целевая модель которой живёт в другом пакете, не раскрывается
вовсе ([[UC-007]]).

### Дальняя сторона связи — это другая метамодель

Предикат скоупа связи пишется против *целевой* модели, поэтому берётся из её
собственной метамодели:

```go
sqlrepo.RelationScope(
    Article_.Comments.Path(),                    // "Comments"
    specs.Predicate(Comment_.Approved.Eq(true))) // "approved" = $1
```

Не `Article_.Comments.Approved` — это атрибут *статьи*, привязанный к
`Comments.Approved`, и он фильтрует статьи по их комментариям через
коррелированный `EXISTS` ([[D-005]]). Полезны обе формы; они отвечают на разные
вопросы.

## Композиция

```go
specs.Where(a).And(b).Or(c).Not()
specs.AllOf(a, b, c)     // AND
specs.AnyOf(a, b, c)     // OR
specs.Not(a)
specs.Lift[User](crud.Eq("Email", "ann@x.io"))   // обычный предикат становится спецификацией
```

Необязательные поля формы тоже остаются декларативными:

```go
filters := specs.AllOf(
    User_.TenantID.Eq(tenant),                 // обязательное условие
    specs.If(q != "", User_.Name.ContainsIgnoreCase(q)),
    User_.OwnerID.EqPtr(ownerID),               // nil: условие отсутствует
    User_.ManagerID.EqOpt(managerID),           // undefined: отсутствует; null: IS NULL
)
```

`If(false, ...)` не добавляет условия. То же относится к nil в `EqPtr` и
undefined в `EqOpt`; null у `EqOpt` намеренно становится `IS NULL`. `AnyOf`,
у которого все члены отсутствуют, не ограничивает запрос, поэтому обязательное
сужение (например, tenant) следует помещать во внешний `AllOf`.

## Criteria builder

`specs.CB` — общий инстанс; нулевой `Builder` тоже работает.

```
Equal        NotEqual     EqualTo      In           NotIn
GreaterThan  GreaterThanOrEqualTo      LessThan     LessThanOrEqualTo
Between      IsNull       IsNotNull
Like         NotLike      LikeIgnoreCase
Contains     StartsWith   EndsWith
ContainsIgnoreCase  StartsWithIgnoreCase  EndsWithIgnoreCase
And          Or           Not          Conjunction  Disjunction   Raw
```

`Like`, `NotLike` и `LikeIgnoreCase` принимают SQL-паттерн как есть: это форма
для вызывающего кода, который намеренно управляет `%` и `_`. Семейства
`Contains`, `StartsWith` и `EndsWith` принимают обычный текст: они экранируют
обратную косую черту, `%` и `_`, сами добавляют нужный wildcard и выводят
диалектный `ESCAPE`, в том числе для SQLite. Варианты `IgnoreCase` используют
переносимое сравнение через `LOWER()`.

---

## Запросы с ним

```go
sp := specs.Executor(Users.Bind(db))

one,   err := sp.FindOne(ctx, User_.Email.Eq("ann@x.io"))   // ErrNotFound / ErrNotUnique
first, err := sp.FindFirst(ctx, adults, crud.OrderBy(User_.Age.Desc()))
list,  err := sp.FindAll(ctx, adults, crud.OrderBy(User_.Age.Desc()))
page,  err := sp.FindPage(ctx, adults, crud.Page(2), crud.Limit(20))
n,     err := sp.CountBy(ctx, adults)
ok,    err := sp.ExistsBy(ctx, User_.Email.Eq("ann@x.io"))
n,     err  = sp.UpdateBy(ctx, adults, UserUpdate{Active: ptr(true)})
n,     err  = sp.DeleteBy(ctx, User_.Active.Eq(false))
```

У `count`, `exists` и `delete` есть суффикс `By`, потому что в Go нет
перегрузки, а простые имена уже заняты репозиторием, который этот декоратор
встраивает.

`specs.Executor` **встраивает** обычный репозиторий, так что `GetByID`,
`Save`, `Update` и всё остальное продолжают работать на том же значении.

`FindOne` возвращает `specs.ErrNotUnique` — который оборачивает
`crud.ErrConflict` — когда совпадает больше одной строки. `FindFirst` берёт
первую вместо этого.

`DeleteBy` и `UpdateBy` отказываются работать с неограниченной декларативной
спецификацией — `specs.ErrUnboundedDelete` и `specs.ErrUnboundedUpdate`. Сюда
входят композиция, схлопнувшаяся в ничто, Criteria `Conjunction`, пустой `NOT
IN`, null/self-проверка primary key и доказуемые моделью двухзначные булевы
формулы вроде `p OR NOT p` или `ID = x OR ID <> x`. Снести или переписать все
строки — это `DeleteAll` и `UpdateAll`, у которых это написано в имени.
`crud.Raw` —
trusted-SQL escape hatch: его нельзя классифицировать в общем виде,
поэтому при намеренном использовании этой силы нужен прямой bulk-метод
репозитория.

Guard также fail-closed для любого непрозрачного bind в спецификации:
`driver.Valuer`, database/sql decimal либо driver-specific value, стабильность
которого нельзя доказать. Он не вызывает пользовательский conversion code при
проверке: тот может быть stateful, вернуть NULL или завершиться ошибкой, а
разные Go-значения могут одинаково закодироваться у драйвера. Булев анализ тоже
имеет budget: слишком широкая/глубокая композиция отклоняется вместо
неограниченной работы в процессе. Когда это намеренно, используйте явный
bulk-метод.

Объявление модели `db:"...,pk"` — это контракт с базой: колонка обязана быть
non-NULL. Для обычных PostgreSQL/MySQL key это автоматически, но SQLite
допускает `NULL` в некоторых legacy primary-key без `INTEGER`; для natural key
SQLite объявляйте `NOT NULL` либо используйте таблицу `STRICT`/`WITHOUT ROWID`.
Bulk guard использует этот non-NULL контракт, доказывая безусловность
primary-key predicate.

## Декоратор никогда не обязателен

Спецификация — это ещё и обычный option:

```go
users.Get(ctx, specs.As(adults), crud.Page(2))
crud.Where(specs.Predicate(adults))
```

## Смотрите также

- [cmd/vv](vv-cli.md) — генерирует структуру атрибутов и метамодель
- [crud](crud.md) — AST предикатов под капотом
- [[UC-007]] пишите типизированные, проверяемые компилятором запросы
- [[D-018]] DTO и метамодели генерируются
