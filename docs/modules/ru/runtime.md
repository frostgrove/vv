# runtime — фоновая работа, у которой есть хозяин

```go
import "github.com/frostgrove/vv/runtime"             // контракт и супервизор
import "github.com/frostgrove/vv/runtime/runtimefx"   // uber/fx: группа раннеров
```

**Модуль:** `runtime` лежит в корневом модуле — только стандартная библиотека.
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

### `runtimefx` — группа раннеров

| | |
|---|---|
| `AsRunner(ctor)` | размечает конструктор, чтобы его `runtime.Runner` попал в группу |
| `Registered` | группа, необязательный логгер и необязательный наблюдатель, как `fx.In` |
| `FailurePolicy` | `ShutDownOnFailure` (по умолчанию) · `KeepRunningOnFailure` |
| `Spec` | `DrainGrace`, `OnFailure` |
| `Supervising(spec)` / `Auto()` | предоставляет супервизор и привязывает его к жизненному циклу |

```go
fx.Options(
    fx.Provide(runtimefx.AsRunner(newDebtSweeper)),
    fx.Provide(runtimefx.AsRunner(newOrphanCollector)),
    runtimefx.Auto(),
)
```

## Вклад в группу и есть активация

Никакого `fx.Invoke(func(*DebtSweeper, *OrphanCollector) {})`. Раннер работает
потому, что он в группе; вызывается только супервизор. Это важно, потому что
пустой invoke несущий, а выглядит как забытый мусор: уберите параметр — и
подметание перестаёт работать в проде, при полностью зелёных тестах. [[D-092]].

## Раннер, который вернулся, — это отказ

`Run` блокируется. Ранний возврат — с ошибкой, с `nil` или паникой — пишется на
имя раннера, логируется, уходит наблюдателю и по умолчанию гасит процесс. `nil`
прячется лучше всех, поэтому у него собственный сентинел `ErrRunnerReturned`.

Деплой, который решил, что мёртвый раннер переживаем, пишет
`OnFailure: runtimefx.KeepRunningOnFailure`. Это именованное значение, а не
забытый `true`.

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
реплики, относится к подсистеме `jobs` и её долговечному расписанию.

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
- [[D-037]] · [[D-092]] · [[FL-028]] · [[UC-026]]
