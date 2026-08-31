# crud/sqlrepo — репозиторий

```go
import "github.com/frostgrove/vv/crud/sqlrepo"
```

**Модуль:** корневой · **Зависит от:** `crud` и стандартной библиотеки

Слой, который говорит на SQL. Опишите модель один раз, привяжите её к
источнику данных — и у вас есть вся поверхность CRUD с семантикой в духе JPA —
поверх любого драйвера, без владения вашим соединением или транзакцией.

> Для сценариев прикладного кода — от первого Get до фильтра через связи —
> используйте [практическое руководство по репозиторию](../../usage-guides/repository.md).
> Эта страница описывает SQL-реализацию, её настройки и инварианты.

---

## Быстрый старт

```go
type User struct {
    ID        int64         `db:"id,pk,auto"`
    TenantID  int64         `db:"tenant_id,immutable"`
    Email     string        `db:"email"`
    Name      string        `db:"name"`
    Age       utils.Opt[int] `db:"age"`
    CreatedAt time.Time     `db:"created_at,generated"`
}

// Имена полей совпадают с моделью 1:1. Указатели опциональны, Opt поддерживает null.
type UserUpdate struct {
    Email *string
    Name  *string
    Age   utils.Opt[int]
}

var Users = sqlrepo.Define[User, int64, UserUpdate]("users")

users := Users.Bind(crudpgx.Open(pool))
```

`Define` **сразу** проверяет теги, тип ID и DTO — так что сломанный маппинг
паникует при инициализации пакета, а не при первом запросе. Используйте
`TryDefine`, если вместо паники нужна ошибка, и `sqlrepo.New`, чтобы пропустить
blueprint и сразу получить привязанный репозиторий.

### Таблица вне пространства имён по умолчанию

Передавайте qualifier и таблицу двумя компонентами identifier:

```go
var Events = sqlrepo.DefineInSchema[Event, int64, EventUpdate](
    "analytics", "events",
)
```

Первый компонент означает схему в PostgreSQL, базу данных в MySQL/MariaDB и
подключённую базу (`ATTACH`) в SQLite. vv цитирует компоненты отдельно:
`"analytics"."events"` в PostgreSQL/SQLite и
`` `analytics`.`events` `` в MySQL.

`Define("analytics.events")` намеренно отклоняется при декларации. Строка с
точкой неоднозначна: это может быть и квалификатор, и буквальная точка в имени
цитируемой таблицы, поэтому vv ничего не угадывает и не разделяет молча.
`TryDefineInSchema` — вариант с возвратом ошибки. Low-level metadata и адаптеры
несут ту же identity как `crud.TableRef`; его компоненты точные и сами могут
содержать точки.

---

## Поверхность

| Метод | Значение |
|---|---|
| `GetByID(ctx, id, opts...)` | одна строка либо `crud.ErrNotFound` |
| `Get(ctx, opts...)` | `crud.PaginatedResponse[M]` |
| `GetAll(ctx, opts...)` | все совпадения; без пагинации, если опция не говорит иначе |
| `First(ctx, opts...)` | первая совпавшая строка либо `crud.ErrNotFound` |
| `Save(ctx, *M)` | нет ключа → `INSERT`, есть ключ → `UPSERT`; возвращает новую сохранённую модель и не меняет аргумент |
| `SaveOnly(ctx, *M)` | та же запись без результата-строки и без мутации аргумента |
| `SaveAll(ctx, []*M)` | пакетный insert/upsert только на запись; модели не меняет |
| `InsertBatch(ctx, []*M, opts...)` | типизированный bulk только insert/write; нативный при явно доступной capability, иначе переносимый |
| `Update(ctx, id, dto, opts...)` | загрузить, сравнить, записать только изменившееся |
| `UpdateAll(ctx, dto, opts...)` | один `UPDATE` по фильтру; возвращает число затронутых строк |
| `Delete(ctx, ids...)` | возвращает, сколько строк исчезло |
| `DeleteAll(ctx, opts...)` | то же самое, с фильтром |
| `Count(ctx, opts...)` / `Exists(ctx, opts...)` | с теми же опциями |
| `Aggregate(ctx, opts...)` | сгруппированные сводки, под тем же сужением, что и чтение |
| `Tx(ctx, fn)` | выполнить в транзакции, присоединившись к уже открытой в `ctx` |
| `Meta()` | привязанная схема и таблица |

### Save возвращает сохранённую строку

Нулевой первичный ключ означает `INSERT`. Ненулевой — `UPSERT` ([[D-011]]).

Ключ с `db:",auto"` исключается из списка колонок. `Save` возвращает отдельную
модель с тем, что действительно сохранила БД: через `RETURNING` на PostgreSQL
и SQLite, либо через insert/upsert и чтение в одной транзакции на диалекте без
него. В результате есть все `generated`-колонки и нормализация триггерами, а
переданный `*M` никогда не меняется.

`SaveOnly` выполняет только запись: не добавляет `RETURNING`, не читает модель
и не меняет аргумент. Используйте его, когда сгенерированные значения не нужны.

`SaveAll` тоже работает только на запись и всегда использует обычный SQL. Он
сохраняет семантику Save: назначенные ключи дают upsert, generated-ключи —
insert. Нативный bulk выделен в отдельный метод, потому что PostgreSQL COPY не
семантически равен INSERT для каждой возможности таблицы.

Bind-бюджет диалекта учитывается автоматически. Помещающаяся пачка остаётся
одним statement. Большая делится по границам строк в порядке вызова, причём все
модели и statements проходят preflight до первой записи. Все чанки входят в
одну транзакцию, поэтому ошибка последнего не оставляет первый закоммиченным.
Пачка с генерируемыми ключами остаётся write-only: `SaveAll` не добавляет
`RETURNING` и не меняет модели ([[D-079]]).

### InsertBatch — create-only нативный bulk

```go
err := users.InsertBatch(ctx, []*User{&a, &b, &c})
```

В отличие от `SaveAll`, `InsertBatch` никогда не делает upsert: строка с
назначенным ключом остаётся insert и может дать conflict. Метод выводит точную
таблицу, колонки и значения из immutable metadata, сохраняет Gate и
fault-декораторы, подключается к ambient executor, не меняет модели и не читает
generated-значения обратно.
Gate один раз авторизует `Create` и инспектирует каждую входящую строку; scope-
only policy без `Inspect` отказывает пачке, а не доверяет значениям, которые не
может проверить. Fault enrichment сохраняет операцию `InsertBatch` и пути полей
из classifier драйвера.

Если точный Source repository напрямую предоставляет `crud.UnsafeBulkInserter`
и metadata даёт insert-колонки, repository выбирает его; crudpgx так
предоставляет PostgreSQL COPY и получает подходящий bound executor как target.
Source остаётся authority, поэтому транзакция не может вновь открыть capability,
которую wrapper скрыл или запретил. Иначе используется тот же preflighted
атомарный bind-budgeted `INSERT` как переносимый fallback. Capability discovery не
угадывает, подходит ли COPY семантике таблицы: для RLS/rewrite rules, особого
pgx encoding или требования обычной INSERT-семантики нужен явный opt-out:

```go
err := users.InsertBatch(ctx, rows, crud.PortableBatch())

var Users = sqlrepo.Define[User, int64, UserUpdate](
    "users",
    sqlrepo.PortableBatch(),
)
```

Для этого write effect `SourceUnwrapper` не обходится. Неизвестный source
wrapper поэтому выбирает переносимый SQL, если сам явно не реализует unsafe
native forwarder. Его `Exec` видит прямой план из одного statement; chunked-план
выполняется на transaction handle. Полный statement tracing должен находиться в
driver/connector либо в явно инструментированных `Begin`/`Tx`. Ошибка
сервера/encoding после начала native-вызова финальна; только before-I/O
`ErrNoBulkInsertSupport` выбирает fallback, поэтому строки не повторяются после
того, как сервер начал обрабатывать native-вызов.

Миграция pre-release API: прежние driver-level `BulkInserter`, `CopyFrom` и
`CopyFromTable` удалены. Application-код переходит на `Repo.InsertBatch`, а
намеренная raw-работа с pgx — на явно unsafe API.

### Raw-предикат и raw-statement — разные границы

`crud.Raw(fragment, args...)` — это predicate-node внутри statement, который
строит repository. Сам фрагмент не проходит field-validation, но repository
по-прежнему владеет таблицей, добавляет permanent- и security scopes, разрешает
ambient executor и запускает обычные fault hooks.

Целый raw-statement обходит границу repository. Прямой `Exec` или `Query` на
`Source`, полученном через `SourceOf`, выполняется на хэндле этого source и
**не** разрешает executor, привязанный в `ctx`. Чтобы raw SQL подключился к той
же транзакции, используйте явные context-aware escape hatches:

```go
result, err := crud.UnsafeExecFor(ctx, source, statement, args...)
rows, err := crud.UnsafeQueryFor(ctx, source, query, args...)
```

Префикс `Unsafe` здесь буквальный: datasource/ambient-transaction routing
сохраняется, но metadata, repository policy, validation и fault-декораторы
обходятся. Бизнесовый SQL прячьте за методом repository; эти функции оставляйте
для намеренных infrastructure-level statements.

### Update — это загрузка, сравнение, запись

`Update` загружает строку, сравнивает DTO с ней и записывает только то, что
изменилось ([[D-010]]). Поле DTO со значением `nil` или `Undefined` вообще не
попадает в запрос; `Opt`, явно установленный в null, пишет `NULL` ([[UC-003]]).

Внутри транзакции загрузка блокирует строку. Вне транзакции два параллельных
обновления могут переплестись — пометьте целочисленную колонку тегом
`version`, и второе обновление будет отклонено с `crud.ErrStaleVersion`
вместо этого ([[UC-009]]).

**Обычные поля DTO применяются всегда**, включая срезы: `nil`-срез `[]byte`
в поле типа `T` пишет `NULL`. Для опциональных колонок используйте `*T` или
`Opt[T]`.

`UpdateAll` — это один запрос по фильтру, и он **не** загружает строку
заранее, поэтому не выполняет ни сравнения, ни продвижения колонки версии.

---

## Настройки

Передаются в `Define`, `TryDefine`, `DefineInSchema`, `TryDefineInSchema` или
`New` и применяются к каждому вызову.

| Настройка | Что делает |
|---|---|
| `DefaultLimit(n)` | размер страницы, когда запрос его не указывает. По умолчанию 20 |
| `MaxLimit(n)` | верхняя граница. Обрезает даже запрос с `Unpaged()` |
| `DefaultSort(orders...)` | сортировка, когда запрос её не указывает |
| `PreloadDepth(n)` | насколько глубоко может идти путь preload. По умолчанию 5 |
| `Scope(pred)` | предикат, добавляемый через AND к каждому чтению и каждой скоупленной записи |
| `RelationScope(path, pred)` | то же самое, на другой стороне связи |
| `SoftDelete(field)` | строки помечаются флагом, а не удаляются, и скрываются из каждого чтения |
| `PortableBatch()` | оставлять каждый `InsertBatch` на обычном bind-budgeted SQL |
| `UnstablePagination()` | убрать добавляемый к каждой сортировке tie-breaker по первичному ключу |
| `IndependentTable()` | оставить дополнительную физическую таблицу модели локальной для этого blueprint |

`Scope` здесь — это сужение **на уровне таблицы**, оно применяется ко всем.
**Per-principal**-форма — это [security](security.md), которая читает
контекст.

Каждое `field` и `path` выше — это имя поля модели или путь связи, и
сгенерированная метамодель отвечает обоими как идентификаторами — см.
[Типизированно или по имени](#типизированно-или-по-имени--работают-обе-формы)
ниже.

### Soft delete

```go
type Doc struct {
    ID        int64                `db:"id,pk,auto"`
    DeletedAt utils.Opt[time.Time] `db:"deleted_at,serverowned,tombstone"`
}
var Docs = sqlrepo.Define[Doc, int64, DocUpdate]("docs")
```

`Delete` и `DeleteAll` пишут флаг вместо удаления строки, и каждое чтение
исключает помеченные строки. `Restore` очищает только tombstone как отдельное
lifecycle-действие; generated wire inputs и generic saves не могут записать это
поле. Security видит `Restore` отдельным action, а не `Update`. Для внешней
модели без vv-тегов эквивалентом остаётся явная настройка blueprint
`SoftDelete("DeletedAt")`. Поле обязано быть nullable timestamp:
`*time.Time`, `Opt[time.Time]` либо совместимым Scanner/Valuer wrapper. При
наличии optimistic-lock version soft delete и restore увеличивают её, закрывая
ABA-окно Delete→Restore. Это свойство запроса, а не декоратора ([[D-031]],
[[UC-016]]).

### Скоупы для связей

Скоуп — это условие `WHERE`, а `WHERE` ограничивает только собственный
`FROM`. Preload — это второй запрос ко второй таблице, поэтому он ничего не
наследует — именно так `?preload=comments` возвращает ровно те строки,
которые скоуп существовал, чтобы скрыть ([[D-007]]).

```go
sqlrepo.RelationScope("Comments", crud.Eq("TenantID", 1))
```

Путь разрешается во время объявления, так что опечатка падает при старте
приложения, а не утекает строками позже. Если и скоуп blueprint'а, и
политика безопасности объявляют сужение для одного и того же пути,
**применяются оба**.

### Канонические и независимые таблицы

По умолчанию всё остаётся декларативным: `Define("users")` полностью проверяет
blueprint и только затем публикует `users` как единственную каноническую цель
связей для `User`. `Define("")` сначала спрашивает `User.TableName()`, затем
использует соглашение о множественном числе. Пустой результат `TableName()` —
ошибка старта, но проверка имени ничего не публикует: явная непустая регистрация
или декларация может исправить имя и повторить попытку. Неуспешный `TryDefine`
не резервирует ни корневую модель, ни цели связей, пройденные до более позднего
ошибочного `RelationScope`, поэтому исправленное объявление не отравлено
предыдущей ошибкой. Имя становится неизменяемым только после успешной
канонической декларации или фактической публикации `Relation.Target`.

`IndependentTable()` — явный низкоуровневый шов для архива, проекции или
catalog probe, который намеренно переиспользует Go-модель, не заменяя её
каноническую таблицу:

```go
var Users = sqlrepo.Define[User, int64, UserUpdate]("users")
var ArchivedUsers = sqlrepo.Define[User, int64, UserUpdate](
    "archived_users", sqlrepo.IndependentTable())
```

Self-связи `ArchivedUsers`, включая циклы, которые позже возвращаются к
`User`, остаются на `archived_users` и не смешивают архивные строки с live.
Другие типы моделей по-прежнему используют свои канонические объявления.
Указывайте `table=users` в теге связи только тогда, когда именно это ребро
намеренно должно покинуть локальный view ([[D-080]]).

### Типизированно или по имени — работают обе формы

Каждая настройка выше принимает имя. Сгенерированная метамодель отвечает теми
же именами, но идентификаторами, поэтому переименование становится ошибкой
сборки, а не объявлением, которое читается как защита и ничего не сужает.

```go
sqlrepo.RelationScope(
    Article_.Comments.Path(),                       // путь
    specs.Predicate(Comment_.TenantID.Eq(1)))       // дальняя сторона
```

Две метамодели, потому что это две разные задачи. **Путь** берётся из группы в
метамодели корня — `Article_.Comments`. **Предикат** пишется против *целевой*
модели, поэтому берётся из её собственной метамодели — `Comment_`, а не
`Article_.Comments`. `Article_.Comments.TenantID` — это атрибут *статьи*,
пишущийся как `Comments.TenantID`, и он фильтрует статьи по их комментариям;
это другой вопрос ([[FL-005]]).

Те же идентификаторы обслуживают и остальное:

```go
sqlrepo.SoftDelete(Doc_.DeletedAt.Name())
sqlrepo.Scope(specs.Predicate(Article_.Hidden.Eq(false)))
sqlrepo.DefaultSort(Article_.CreatedAt.Desc())
crud.Preload(Article_.Comments.Path(), Article_.Author.Path())
```

`Name()` отвечает каноническим именем атрибута, `Path()` — каноническим путём
связи. Ничего не объявлено устаревшим: имя, известное только во время
выполнения — пришедшее по проводу — остаётся строкой, и неизвестное имя
получает отказ, а не превращается в тихо исчезающее условие ([[D-013]],
[[UC-007]]).

Группа связи несёт хэндл только там, где генератор её раскрыл, а этим управляет
`-depth`. И поскольку хэндл встроен, колонка `Path` у целевой модели затеняет
метод — сгенерированный файл говорит об этом в doc-комментарии этой группы, а
`RelPath()` — форма, которую ничто не затеняет.

---

## Декораторы

`Bind` принимает middleware. Первый в списке оказывается самым внешним.

```go
users := specs.Executor(Users.Bind(db,
    security.Gate(policy),
    faults.Enrich[User, int64](faults.WithProbe(probe.Full(cat))),
))
```

Каждый параметр типа выводится из аргумента, так что места вызова остаются
свободны от явных дженериков. Написать свой декоратор — это встроенный
интерфейс и один метод:

```go
type auditing struct{ crud.Core[User, int64] }

func (a auditing) Save(ctx context.Context, u *User) (User, error) {
    log.Println("saving", u.Email)
    return a.Core.Save(ctx, u)
}

func Log() crud.Middleware[User, int64] {
    return func(next crud.Core[User, int64]) crud.Core[User, int64] {
        return auditing{next}
    }
}
```

Декоратор видит `crud.Core[M, ID]` — два параметра типа, а не три — именно
это делает сигнатуру middleware пригодной для записи ([[D-001]]).
Трёхпараметрический `crud.Repo[M, ID, U]` — это фасад; потребитель держит
указатель `*crud.Repo[M, ID, U]`.

**Новый метод на этом стыке — это обязательство для декораторов**
([[D-030]]): добавление метода в `Core` означает, что каждый декоратор в
дереве должен его прокидывать, иначе он молча обходит gate.

---

## Привязка к источнику данных

```go
db := crudpgx.Open(pool)                 // pgx v5
db := crudsql.Postgres(sqlDB)            // database/sql
db := crudsql.MySQL(sqlDB)
db := crudsql.MariaDB(sqlDB)             // тот же диалект, другие номера ошибок
db := crudsql.SQLite(sqlDB)
db := crud.ReadWrite(primary, replica)   // чтения — в replica, записи — в primary

users := Users.Bind(db)
```

Один blueprint можно привязать много раз — ко второй базе данных, к
тестовому рекордеру, к паре реплик. `Define` — это объявление; `Bind` — это
подключение.

## Транзакции

```go
err := users.Tx(ctx, func(ctx context.Context) error {
    u, err := users.GetByID(ctx, 42)
    if err != nil { return err }
    _, err = users.Update(ctx, u.ID, UserUpdate{Name: ptr("new")})
    return err
})
```

`Tx` **присоединяется** к транзакции, уже находящейся в контексте, а не
вкладывается в неё: внешний владелец сохраняет контроль над commit и
rollback, и `fn` не может откатиться самостоятельно. `crud.InTx(ctx, db, fn)`
делает то же самое сразу для нескольких репозиториев. Для настоящей
вложенности `Begin` даёт savepoint — нативно на pgx, через `SAVEPOINT` на
`database/sql` ([[FL-009]]).

`SaveAll`, переносимый `InsertBatch` и `Delete(ids...)` используют то же
правило, когда bind-бюджет требует нескольких statements. Они присоединяются к
ambient-транзакции либо открывают одну; источник, который не может дать ни одну
атомарную границу, возвращает `crud.ErrNoTxSupport` до первого чанка. Для одного
statement лишняя транзакция не открывается. Нативный `InsertBatch` разрешается
точным Source и получает resolved executor как target, поэтому pgx COPY
подключается к той же ambient-транзакции, а не сбегает в pool и не обходит
Source wrapper.
Привязанный non-transaction executor не является атомарной границей и не
используется для chunked-плана: sqlrepo открывает транзакцию от своего source.
Если connection-local state должен сохраниться между чанками, заранее начните и
привяжите транзакцию либо привяжите repository к самому session source.

## Острые углы

- **Число затронутых строк расходится.** MySQL сообщает 0 для `UPDATE`,
  который ничего не изменил, и в зависимости от конфигурации считает
  *совпавшие*, а не *изменённые* строки. Поэтому `ErrNotFound` никогда не
  выводится из `n == 0` на пути записи.
- **`DeleteAll` перед удалением выбирает свои жертвы с опциями вызывающего**,
  так что сужение декоратора применяется и к удалению, а не только к выборке
  ([[D-026]]).
- **`Distinct()` принудительно включает первичный ключ в проекцию**, если он
  нужен сортировке, потому что `SELECT DISTINCT` и `ORDER BY` по невыбранной
  колонке — ошибка на PostgreSQL ([[D-024]]).
- **Неизвестное поле — это отказ**, а не отброшенное условие ([[D-013]]).
- **SQL детерминирован** — одни и те же опции дают один и тот же запрос,
  байт в байт ([[D-014]]). Именно это делает его тестируемым через
  [crudtest](crudtest.md).
- **Bind-лимиты проверяются до datasource.** `In` / `InAny` и остальные прямые
  Go-предикаты делят один statement-wide бюджет диалекта. Слишком большой
  statement получает типизированный отказ схемы. `SaveAll`, переносимый
  `InsertBatch` и `Delete(ids...)` чанкуются, потому что их смысл сохраняется;
  произвольный предикат — нет ([[D-079]]).
- **Пригодность COPY — это декларация, а не догадка.** Bare pgx-source
  предоставляет нативный bulk по умолчанию. Для RLS/rewrite rules, особого
  encoding или полной наблюдаемости statement middleware выбирайте per-call
  `crud.PortableBatch()` либо blueprint `sqlrepo.PortableBatch()`.
- **Регистрация таблицы типизирована.** `RegisterTable` принимает struct-модель,
  а не `*Model`, scalar или interface. Конфликтующее либо уже опубликованное
  имя получает явный отказ ([[D-080]]).
- **Qualified identity таблицы структурирована.** Строка с точкой в
  `Define`/`TableName` — ошибка декларации. Используйте `DefineInSchema`; в
  relation override это `schema=...,table=...`, а для many-to-many join — ещё
  `joinSchema=...`.

## Колоночный `DEFAULT` не срабатывает

vv пишет каждую отображённую колонку, поэтому построенный им INSERT называет их
все — и строка, созданная без значения для одной из них, сохранит нулевое
значение Go, а не `DEFAULT` колонки. Значение по умолчанию достаётся только тем
строкам, которые база создаёт сама.

Это первый сюрприз, на который натыкается большинство новичков, и он следует из
[[D-014]]: один и тот же вызов обязан компилироваться в один и тот же стейтмент,
а стейтмент, опускающий те колонки, которые случайно оказались нулевыми, этого
не делал бы. Там, где значением должен владеть сервер, пометьте колонку
`generated` — тогда vv не включит её в INSERT и прочитает обратно — или
заполните её в хуке `BeforeSave`.

## См. также

- [crud](crud.md) — словарь, который рендерит этот пакет
- [specs](specs.md) · [security](security.md) · [faults](faults.md) — декораторы
- [crudtest](crudtest.md) — проверяйте запрос без базы данных
- [[FL-001]] [[FL-002]] [[FL-003]] [[FL-004]] — чтение, патч, сохранение, объявление
</content>
