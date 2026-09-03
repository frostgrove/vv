# runtime — фоновая работа, у которой есть хозяин

```go
import "github.com/frostgrove/vv/runtime"              // контракт и супервизор
import "github.com/frostgrove/vv/runtime/runtimefx"    // uber/fx: группа раннеров
import "github.com/frostgrove/vv/runtime/runtimecheck" // сторож D-092 над любым деревом
```

**Модуль:** `runtime` и `runtimecheck` лежат в корневом модуле — только
стандартная библиотека.
`runtimefx` — отдельный модуль, потому что берёт uber/fx ([[D-033]])
· **Зависит от:** ничего

Подметальщик, сборщик и потребитель очереди — одна и та же форма: нечто, что
блокируется, пока процесс не остановят. Этот пакет даёт форме имя, запускает её
и делает умерший воркер таким же громким, как и незапущенный.

---

## Что вы получаете

### `runtime` — контракт

| | |
|---|---|
| `Runner` | `Name() string`, `Run(ctx) error` — `Run` блокируется, пока жив контекст |
| `Drainer` | необязательный: `Drain(ctx) error`, вызывается до отмены |
| `Readier` | необязательный: `Ready(ctx) error` |
| `Declaring` / `DeclarationOf` | необязательный: что раннер обещает, и безопасный способ спросить |
| `Declaration` | `Placement` (`PerReplica` · `Singleton`) и `Durability` (`NonDurable` · `Durable`) |
| `PerReplicaTimer` | пара, которую отвечает процессный тикер |
| `Phase` | `PhaseIdle` · `PhaseRunning` · `PhaseStopped` · `PhaseFailed` |
| `RunnerState` | имя, декларация, фаза, ошибка, начало и конец |
| `Observer` / `ObserverFunc` | шов: каждый переход, с изоляцией паники |

### `runtime` — супервизор

| | |
|---|---|
| `Spec` | `Runners`, `DrainGrace`, `Logger`, `Observer` |
| `NewSupervisor(spec)` | явный конструктор; отказывает сразу по nil, безымянным и одноимённым раннерам |
| `Auto(runners…)` | то же самое с умолчаниями |
| `Start(ctx)` / `Stop(ctx)` | две половины жизненного цикла |
| `States()` | состояние каждого раннера, по имени |
| `Ready(ctx)` | упавшие раннеры плюс ответ каждого `Readier`, собранные вместе |
| `DefaultDrainGrace` | 15s |
| `ErrDuplicateRunner` · `ErrRunnerReturned` · `ErrRunnerPanicked` · `ErrDrainDeadline` · `ErrNotRunning` · `ErrAlreadyStarted` · `ErrStillStopping` | |

### `runtime` — периодический раннер

| | |
|---|---|
| `PeriodicSpec` | `Name`, `Interval`, `Timeout`, `Immediate`, `Pass`, `Logger`, `Ticks` |
| `NewPeriodic(spec)` | явный конструктор |
| `Every(name, interval, pass)` | короткая форма, те же отказы |
| `Ticker` / `Ticks` / `SystemTicks` | шов часов, чтобы тест не ждал |

```go
sweeper, err := runtime.NewPeriodic(runtime.PeriodicSpec{
    Name:     "translation-debt",
    Interval: 5 * time.Minute,
    Timeout:  time.Minute,
    Pass:     debt.Sweep,
})
```

### `runtime` — луп, которым владеет компонент

| | |
|---|---|
| `LoopSpec` | `Name`, `Run`, `Logger`, `Observer`, `StopGrace` |
| `NewLoop(spec)` | супервизор на одного; он не падает, а отказы приходят на `Start` |
| `Start(ctx)` / `Stop(ctx)` | две половины компонента — `Stop` дожидается горутины |
| `State()` | `RunnerState` этого лупа, для хозяина, которому за него отвечать |

```go
this.loop = runtime.NewLoop(runtime.LoopSpec{
    Name:     "core.realtime.listener",
    Logger:   this.log,
    Observer: failure,
    Run:      this.listen,
})
```

### `runtimefx` — группа раннеров

| | |
|---|---|
| `AsRunner(ctor)` | размечает конструктор, чтобы его `runtime.Runner` попал в группу |
| `Registered` | группа, необязательный логгер и необязательный наблюдатель, как `fx.In` |
| `FailurePolicy` | `ShutDownOnFailure` (по умолчанию) · `KeepRunningOnFailure` |
| `Spec` | `DrainGrace`, `OnFailure` |
| `Supervising(spec)` / `Auto()` | предоставляет супервизор и привязывает его к жизненному циклу |
| `ShuttingDownOnFailure(shutdowner, log)` | та же политика отказа отдельно — для компонента с `runtime.Loop` |

```go
fx.Options(
    fx.Provide(runtimefx.AsRunner(newDebtSweeper)),
    fx.Provide(runtimefx.AsRunner(newOrphanCollector)),
    runtimefx.Auto(),
)
```

### `runtimecheck` — сторож

| | |
|---|---|
| `Activation` | `File`, `Line`, `Name` — один `fx.Invoke`, активирующий побочным эффектом |
| `EmptyInvokeActivations` | `(root string) ([]Activation, error)` — обойти дерево со списком пропусков по умолчанию |
| `Scanner` | `SkipDirectory func(name string) bool` — тот же обход с собственным представлением о том, что не исходники |
| `SkipsHiddenAndVendored` | по умолчанию: `testdata`, `vendor`, `node_modules` и любой каталог на точку или подчёркивание |

## Вклад в группу и есть активация

Никакого `fx.Invoke(func(*DebtSweeper, *OrphanCollector) {})`. Раннер работает
потому, что он в группе; вызывается только супервизор. Это важно, потому что
пустой invoke несущий, а выглядит как забытый мусор: уберите параметр — и
подметание перестаёт работать в проде, при полностью зелёных тестах. [[D-092]].

Держит это `runtime/runtimecheck`. `EmptyInvokeActivations(root)` разбирает
дерево пакетами и сообщает про каждый `fx.Invoke`, у функции которого пустое
тело, — и про литерал, и про именованную `func reached(*Client) {}`, которую
пофайловый обход пропустил бы: invoke и функция обычно лежат в разных файлах
одного пакета. `Scanner{SkipDirectory: …}` — тот же обход, когда у дерева
вызывающего свои каталоги, которые исходниками не являются.

`runtime/activation_test.go` наводит его на этот репозиторий. Карта `tolerated`
перечисляет файлы, которые всё ещё так делают, и причину; запись, переставшая
совпадать, роняет тест, а не остаётся лежать.

Сканер экспортирован, потому что инвариант принадлежит владельцу любого
композиционного корня, а не только этому дереву. Приложение держит его над своим
кодом так:

```go
func TestNothingInTheTreeIsActivatedByAnEmptyInvoke(t *testing.T) {
    found, err := runtimecheck.EmptyInvokeActivations("../..")
    if err != nil {
        t.Fatal(err)
    }
    if len(found) > 0 {
        t.Fatalf("активировано пустым fx.Invoke: %v", found)
    }
}
```

## Раннер, который вернулся, — это отказ

`Run` блокируется. Ранний возврат — с ошибкой, с `nil` или паникой — пишется на
имя раннера, логируется, уходит наблюдателю и по умолчанию гасит процесс. `nil`
прячется лучше всех, поэтому у него собственный сентинел `ErrRunnerReturned`.

Деплой, который решил, что мёртвый раннер переживаем, пишет
`OnFailure: runtimefx.KeepRunningOnFailure`. Это именованное значение, а не
забытый `true`.

## Луп компонента тоже поднадзорный

Шина, держащая одно соединение `LISTEN`, и кэш, освобождающий протухшее, — это
фоновая работа, и ни та ни другая не относится к группе процесса: луп стартует
вместе с компонентом, а граф, в котором компонент есть, а супервизора нет, держал
бы луп, который никто не запускает. `runtime.Loop` — это тот самый случай:
супервизор на одного, который запускает `Start` самого компонента и
останавливает его `Stop`. У лупа остаётся имя, перехваченная паника, ранний
возврат, записанный как `ErrRunnerReturned`, и отказ, ушедший наблюдателю;
`runtimefx.ShuttingDownOnFailure` — политика отказа группы отдельно, для
хозяина, который хочет, чтобы мёртвый луп стоил процессу столько же, сколько
мёртвый раннер. Чего исключение не разрешает — это `go this.run()`. [[D-092]].

## Контекст старта, порядок drain, отсрочка

`Start` заводит собственный контекст. Контекст `OnStart` у fx отменяется, как
только старт закончился, и воркер, получивший его, останавливается ровно в тот
момент, когда стал готов, — что выглядит в точности как незапущенный воркер.

`Stop` сначала сливает, потом отменяет: раннеру, которому сказали закончить,
хватает времени зафиксировать последнюю единицу работы, а отменённому первым —
только бросить её. Всё, что не вернулось за `DrainGrace`, названо в
`ErrDrainDeadline`.

Супервизор можно запустить снова после остановки, и каждый `Start` открывает
новое поколение: сбрасывается и флаг остановки, по которому ожидаемый возврат
отличают от тихой смерти, и состояние, с которым раннеры закончили прошлое
поколение. Иначе одна остановка выключает `ErrRunnerReturned` на весь остаток
жизни процесса — любая следующая смерть записывается как штатный останов, а
готовность продолжает отвечать «готов». Старт, пока живы горутины прошлого
поколения — тот самый случай, о котором докладывает `ErrDrainDeadline`, — это
`ErrStillStopping`, а не вторая копия каждого раннера.

## Per-replica, non-durable — и сказано вслух

`Periodic` — это тикер в этом процессе. Три реплики дают три прохода, а проход,
прерванный деплоем, потерян. Часто это правильно и никогда не очевидно из места
вызова, поэтому раннер отвечает `Declaration{PerReplica, NonDurable}`, и
состояние супервизора это несёт.

Работа, которая обязана случиться один раз на кластер или пережить потерю
реплики, относится к подсистеме `jobs` и её долговечному расписанию. `jobsfx`
отдаёт в эту же группу свой пул воркеров и планировщик — под именами
`vv.jobs.workers` и `vv.jobs.scheduler` — когда spec говорит, что деплоймент
исполняет эти роли ([[D-108]]). `jobspgfx` отдаёт туда же уборку retention под
именем `vv.jobspg.retention`, если настройки её не выключили: это тикер на
реплику, поэтому он здесь, а не в долговечном расписании.

Упавший или паникнувший проход докладывается, а расписание продолжается —
подметание, не достучавшееся до базы в 03:00, обязано пройти в 03:05, — а
проход, вышедший за свой бюджет, отменяется, а не задерживает все следующие.

## Готовность, не зная, что такое health

`Supervisor.Ready` собирает отказы вместе с ответами всех `Readier` и
возвращает ошибку. `runtime` не импортирует `health`, а `health` не импортирует
`runtime`: превращает одно в `health.Contribution` композиционный корень.

```go
healthfx.AsCheck(func(supervisor *runtime.Supervisor) health.Contribution {
    return health.Contribution{
        Name: "runtime.workers", Code: "workers",
        Importance: health.Degrading,
        Probe:      health.ProbeFunc(supervisor.Ready),
    }
})
```

## Смотрите также

- [health](health.md) — реестр, до которого дотягивается эта проверка
- [app](app.md) — остальное, что собирает `main()`
- [[D-037]] · [[D-092]] · [[D-108]] · [[FL-028]] · [[UC-026]]
