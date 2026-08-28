# specs — типизированные запросы без строковых полей

~~~go
import "github.com/frostgrove/vv/crud/decorators/specs"
~~~

<code>specs</code> — необязательный слой над обычным репозиторием. Он превращает
переиспользуемый фильтр в значение <code>Specification[M]</code> и даёт
метамодель, которую проверяет компилятор.

Если нужен общий путь «модель → генерация → Get/Save», начните с
[руководства по репозиторию](../../usage-guides/repository.md).

## За минуту: что это даёт

Без метамодели разовый запрос выглядит так:

~~~go
products.Get(ctx,
    crud.Where(crud.Gte("Price", 10_000)),
    crud.Where(crud.ContainsIgnoreCase("Name", q)),
)
~~~

Это нормально для простого случая. Но <code>"Price"</code> — строка: опечатку,
удалённое поле и неверный тип значения Go не увидит до запуска.

С генератором тот же запрос:

~~~go
products.Get(ctx,
    specs.As(specs.AllOf(
        Product_.Price.Gte(10_000),
        Product_.Name.ContainsIgnoreCase(q),
    )),
    crud.OrderBy(Product_.CreatedAt.Desc()),
)
~~~

<code>Product_.Price.Gte</code> принимает только тип цены, а
<code>Product_.Name</code> предлагает строковые операции. Переименовали поле и
перегенерировали <code>vv_gen.go</code> — старые обращения становятся ошибками
компиляции.

| Когда | Что использовать |
|---|---|
| одно условие, нигде больше не повторится | <code>crud.Where(crud.Eq("Field", value))</code> |
| фильтр собирается из необязательных частей | <code>specs.AllOf</code> / <code>specs.If</code> / <code>EqPtr</code> / <code>EqOpt</code> |
| правило нужно в разных сервисах | именованную <code>Specification[M]</code> |
| нужны связи, типизированная сортировка и preload-пути | метамодель <code>Product_</code> |

## ProductAttrs и Product_: что именно генерируется

Для модели:

~~~go
type Product struct {
    ID            uuid.UUID
    TenantID      uuid.UUID
    CategoryID    *uuid.UUID
    BrandID       *uuid.UUID
    Slug          string
    Name          string
    Price         int64
    Published     bool
    AvailableFrom time.Time
    CreatedAt     time.Time
}
~~~

<code>vv generate</code> записывает в <code>vv_gen.go</code> примерно следующее:

~~~go
// Техническая форма полей модели — обычно её не пишут и не меняют руками.
type ProductAttrs struct {
    ID        specs.Attr[Product, uuid.UUID]
    Name      specs.Str[Product]
    Price     specs.Ord[Product, int64]
    CreatedAt specs.Cmp[Product, time.Time]
    // ... остальные поля Product
}

// Готовое значение этой формы.
var Product_ = specs.Metamodel[Product, ProductAttrs]()
~~~

<code>ProductAttrs</code> — описание **какие операции допустимы для каждого
поля**. <code>Product_</code> (то, что иногда называют <code>Entity_</code>) —
один готовый экземпляр этого описания. Суффикс <code>_</code> — соглашение,
взятое из JPA; это не отдельный тип модели, не таблица и не объект из БД.

~~~go
Product_.Name                       // ссылка на поле Name
Product_.Name.Contains("chair")    // Specification[Product]
Product_.Price.Gte(10_000)          // Specification[Product]
Product_.CreatedAt.Desc()           // crud.Order
Product_.Name.Name()                // "Name" — для API, ожидающего имя поля
~~~

Метамодель валидируется при инициализации пакета: если сгенерированный тип
говорит, что <code>Price</code> — <code>int64</code>, а модель уже изменилась,
приложение сразу назовёт несовпавшее поле. Если тип значения неверен в самом
вызове, код вообще не скомпилируется.

> Не создавайте <code>ProductAttrs</code> вручную в прикладном коде. Поменяли
> модель — выполните <code>make generate</code> и закоммитьте обновлённый
> <code>vv_gen.go</code>.

## Атрибуты: какие методы появятся

Генератор выбирает самый узкий тип по Go-типу поля.

| Поле модели | Генерируемый attr | Полезные методы |
|---|---|---|
| <code>bool</code>, UUID, enum, struct-значение | <code>specs.Attr[M, T]</code> | <code>Eq</code>, <code>Ne</code>, <code>In</code>, <code>NotIn</code>, <code>IsNull</code>, <code>NotNull</code>, <code>Asc</code>, <code>Desc</code> |
| <code>int</code>, <code>float</code> и другие <code>cmp.Ordered</code> типы | <code>specs.Ord[M, T]</code> | всё выше + <code>Gt</code>, <code>Gte</code>, <code>Lt</code>, <code>Lte</code>, <code>Between</code> |
| <code>time.Time</code> и другой сравнимый, но не <code>cmp.Ordered</code> тип | <code>specs.Cmp[M, T]</code> | равенство, сортировка и диапазоны |
| <code>string</code> | <code>specs.Str[M]</code> | всё у <code>Ord</code> + <code>Contains</code>, <code>StartsWith</code>, <code>EndsWith</code>, <code>Like</code> и варианты <code>IgnoreCase</code> |

<code>Contains</code>, <code>StartsWith</code> и <code>EndsWith</code> принимают
обычный пользовательский текст: они сами экранируют <code>%</code>,
<code>_</code> и обратную косую черту. <code>Like</code> принимает SQL-паттерн
буквально — используйте его, только когда wildcard нужен намеренно.

## Пишите спецификации как маленькие правила

Метод атрибута уже возвращает <code>Specification[M]</code>. Поэтому можно
собрать правило один раз и передавать дальше как обычное значение:

~~~go
func AvailableProducts(now time.Time) specs.Specification[Product] {
    return specs.AllOf(
        Product_.Published.Eq(true),
        Product_.AvailableFrom.Lte(now),
    )
}

func InPriceRange(min, max *int64) specs.Specification[Product] {
    return specs.AllOf(
        PriceAtLeast(min),
        PriceAtMost(max),
    )
}

func PriceAtLeast(min *int64) specs.Specification[Product] {
    if min == nil {
        return nil
    }
    return Product_.Price.Gte(*min)
}

func PriceAtMost(max *int64) specs.Specification[Product] {
    if max == nil {
        return nil
    }
    return Product_.Price.Lte(*max)
}
~~~

Все следующие формы возвращают ту же <code>Specification[Product]</code>:

~~~go
specs.AllOf(a, b, c)        // a AND b AND c
specs.AnyOf(a, b, c)        // a OR b OR c
specs.Not(a)                // NOT a
specs.Where(a).And(b).Or(c).Not()
specs.If(enabled, a)        // nil, если enabled == false
specs.Lift[Product](crud.Eq("Published", true))
~~~

<code>nil</code>-спецификация в <code>AllOf</code>/<code>AnyOf</code> просто не
добавляет условия. Это удобно для формы поиска, но обязательный scope (например,
tenant) кладите отдельным внешним условием:

~~~go
filter := specs.AllOf(
    Product_.TenantID.Eq(tenantID), // всегда есть
    specs.If(q != "", Product_.Name.ContainsIgnoreCase(q)),
    Product_.CategoryID.EqPtr(categoryID),
    Product_.BrandID.EqOpt(brandID),
)
~~~

<code>EqPtr(nil)</code> ничего не добавляет. <code>EqOpt</code> сохраняет три
состояния <code>utils.Opt</code>: undefined не добавляет условие, null означает
<code>IS NULL</code>, значение означает <code>=</code>.

## Передайте спецификацию обычному репозиторию

Спецификация становится обычной <code>crud.Option</code> через
<code>specs.As</code>:

~~~go
filter := specs.AllOf(
    AvailableProducts(clock.Now()),
    Product_.Name.ContainsIgnoreCase(q),
)

page, err := products.Get(ctx,
    specs.As(filter),
    crud.OrderBy(Product_.Price.Asc(), Product_.ID.Asc()),
    crud.Page(1),
    crud.Limit(24),
)

one, err := products.First(ctx,
    specs.As(Product_.Slug.Eq(slug)),
    crud.Unsorted(),
)
~~~

Это не требует декоратора. Если хотите вызвать <code>crud.Where</code> сами,
используйте <code>crud.Where(specs.Predicate(filter))</code> — результат такой же.

## Или включите specs.Executor

<code>specs.Executor</code> встраивает исходный репозиторий и добавляет
JPA-подобные методы. Обычные <code>GetByID</code>, <code>Save</code>,
<code>Update</code> и остальные никуда не исчезают.

Генерируемая фабрика возвращает указатель, чтобы его было удобно отдать в DI.
<code>specs.Executor</code> принимает сам маленький value-объект
<code>crud.Repo</code>, поэтому в этом одном месте передайте
<code>*product.NewProductRepository(src)</code>.

~~~go
products := specs.Executor(*product.NewProductRepository(src))

one, err := products.FindOne(ctx, Product_.Slug.Eq(slug))
first, err := products.FindFirst(ctx, AvailableProducts(now),
    crud.OrderBy(Product_.CreatedAt.Desc()))
items, err := products.FindAll(ctx, AvailableProducts(now))
page, err := products.FindPage(ctx, AvailableProducts(now),
    crud.Page(2), crud.Limit(24))

n, err := products.CountBy(ctx, AvailableProducts(now))
ok, err := products.ExistsBy(ctx, Product_.Slug.Eq(slug))
~~~

| Метод | Отличие от базового репозитория |
|---|---|
| <code>FindOne</code> | требует ровно одну строку: <code>crud.ErrNotFound</code> или <code>specs.ErrNotUnique</code> |
| <code>FindFirst</code> | берёт первую подходящую строку; передавайте сортировку |
| <code>FindAll</code> / <code>FindPage</code> | то же, что <code>GetAll</code> / <code>Get</code>, но спецификация отдельным аргументом |
| <code>CountBy</code> / <code>ExistsBy</code> | count/existence для спецификации |
| <code>UpdateBy</code> / <code>DeleteBy</code> | bulk-write по спецификации; отказываются от пустого или доказуемо широкого фильтра |

Для явного «обновить всё» по options остаются базовые <code>UpdateAll</code> и
<code>DeleteAll</code>. У <code>UpdateBy</code>/<code>DeleteBy</code> защита
намеренная: она не даст случайно сделать write по всей таблице, если
необязательные части фильтра схлопнулись в ничто.

## Связи: фильтровать родителей, загружать детей — разные операции

Пусть <code>Article</code> имеет <code>Author</code> и <code>Comments</code>.
Генератор добавит вложенные группы в <code>Article_</code>:

~~~go
Article_.Author.Name.Eq("Ann")          // спецификация Article
Article_.Comments.Approved.Eq(true)     // спецификация Article
Article_.Comments.Path()                // "Comments", но без строкового literal
Article_.Comments.Author.Path()         // "Comments.Author"
~~~

Первый и второй вызов **отбирают статьи**. Они не наполняют поля
<code>Author</code> или <code>Comments</code> в ответе. Для загрузки данных нужен
preload:

~~~go
articles, err := articlesRepo.GetAll(ctx,
    specs.As(Article_.Comments.Approved.Eq(true)),
    crud.Preload(
        Article_.Author.Path(),
        Article_.Comments.Path(),
    ),
)
~~~

<code>Preload</code> грузит каждую связь батчем, а не N+1 запросом. Условие
именно для загружаемых детей пишется от метамодели целевой модели:

~~~go
crud.PreloadWhere(
    Article_.Comments.Path(),
    specs.As(Comment_.Approved.Eq(true)),
)
~~~

Почему не <code>Article_.Comments.Approved</code>? Потому что это условие для
**статей, у которых есть** такой comment. <code>Comment_.Approved</code> в
<code>PreloadWhere</code> — условие для **самого списка загружаемых comments**.

Связи раскрываются генератором до <code>-depth</code> (по умолчанию 2) и не идут
по циклу обратно в уже встреченную модель. Если поля связи в <code>Article_</code>
нет, увеличьте глубину и перегенерируйте.

## Когда строки всё-таки оправданы

Метамодель покрывает прикладные запросы. Для динамического админ-фильтра,
приходящего из JSON, естественно использовать уже проверенный парсер
<code>query</code>, а для ручной конструкции — <code>crud</code>:

~~~go
filter := crud.And(
    crud.Gte("CreatedAt", since),
    crud.In("Status", "draft", "published"),
)
products.Get(ctx, crud.Where(filter))
~~~

Полная literal-форма <code>specs.Of</code>/<code>Root</code>/<code>Builder</code>
нужна редко, когда поле действительно вычисляется в рантайме:

~~~go
byField := specs.Of[Product](func(root specs.Root[Product], cb specs.Builder) crud.Predicate {
    return cb.Equal(root.Get(fieldFromConfig), value)
})
~~~

<code>crud.Raw</code> — escape hatch для доверенного SQL. Он не валидирует имена
и не безопасен для пользовательского ввода; не подставляйте в него строки из
HTTP-параметров.

## Генерация и типичные ошибки

~~~bash
go run github.com/frostgrove/vv/cmd/vv generate -dir ./src/app
make generate
git diff --exit-code
~~~

| Симптом | Что проверить |
|---|---|
| <code>Product_</code> не существует | модель должна быть экспортирована и лежать в <code>model.go</code>, <code>*.model.go</code> или <code>*_model.go</code>; затем выполните generate |
| Нет поля связи в <code>Product_</code> | связь нужна в модели, а глубина генерации должна её достигать (<code>-depth</code>) |
| panic при старте про metamodel | модель и старый <code>vv_gen.go</code> разошлись; перегенерируйте |
| тип не компилируется в <code>Eq</code>/<code>Gte</code> | это полезная проверка: возьмите значение типа поля или используйте подходящий attr-метод |
| Нужен <code>Path()</code>, но метод затенён колонкой <code>Path</code> у цели | вызовите <code>RelPath()</code> — он всегда доступен |

## Дальше

- [Репозиторий от модели до транзакции](../../usage-guides/repository.md)
- [crud](crud.md) — options, предикаты, пагинация, транзакционный executor
- [crud/sqlrepo](sqlrepo.md) — <code>Define</code>, <code>Scope</code>, soft delete, реплики
- [cmd/vv](vv-cli.md) — флаги генератора
