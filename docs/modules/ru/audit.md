# audit — декларативные защищённые свидетельства

```go
import (
    "github.com/frostgrove/vv/audit"
    "github.com/frostgrove/vv/audit/auditcrud"
    "github.com/frostgrove/vv/audit/auditmemory"
)
```

**Модуль:** `audit`, `auditmemory`, `audittest` и `auditcrud` входят в корневой
модуль и не добавляют сторонних зависимостей. `audit/auditpg` — отдельный
вложенный модуль, потому что он владеет выбором PostgreSQL-драйвера.

`audit` записывает только явно разрешённые бизнес-факты и ревизии сущностей.
Декларация заранее фиксирует смысл, retention, consequence, контекст, кодеки
полей, privacy class и способ хранения. Writer получает уже каноническое
свидетельство, а не значения из context вызывающего кода или произвольный граф
объектов.

Текущая поддерживаемая граница — **alpha для разработки приложений**. Уже можно
использовать ручную запись, grouping, idempotency/reconciliation, memory store,
транзакционный CRUD, PostgreSQL persistence и публичную одностраничную историю.
Protected history, возобновляемые cursor, attempts, reconstruction, corrections,
holds и purge planning пока не поставлены; наличие публичного vocabulary не
означает поддержку capability.

---

## Что вы получаете

| | |
|---|---|
| `Declare` | Типизированное событие с закрытыми resource/action/outcome/reason и allowlist полей |
| `Define` | Типизированная политика сущности поверх существующего `crud.Meta`, включая subject identity и reconstructable fields |
| `DeclareOperation` | Именованная операция и точный набор допустимых event/entity actions |
| `Compile` · `Lineage` | Неизменяемый каталог и его append-only retained lineage |
| `New` | Recorder из одного catalog set, writer, trusted context resolver, semantic digester и identity keyring |
| `Recorder.Capture` | Одно самостоятельное событие с немедленным `Committed` receipt |
| `Recorder.Record` | Явные operation/idempotency options и результат для retry/reconciliation |
| `Recorder.Within` · `Stage` | Одна operation revision из нескольких объявленных фактов; join только к точной source-bound root transaction |
| `auditmemory` | Конкурентный in-process writer/log с явной deployment activation и неизменяемым readback catalog mutations |
| `auditcrud.Secured` | Закрытый security-first CRUD terminal для create, assigned/unassigned save, update, hard/soft delete и restore |
| `NewHistory` | Отдельно авторизованная ограниченная публичная история по resource, subject, event type/target и operation type/instance |
| `audittest.BasicHistory` | Повторно используемый conformance suite истории для реализации store |
| `auditpg` | Точная schema readiness, catalog activation/readback, durable append/reconciliation, transaction joining и базовая история |

## Сначала декларация

```go
type StatusPublished struct {
    Slug string
    At     time.Time
}

var AuditContext = audit.ContextFacts(
    audit.ScopeFact(
        audit.ContextRequired,
        audit.Provenances(audit.Verified),
        audit.Public,
        audit.AsPlaintext,
    ),
    audit.GeneratedOperationFact(audit.Public, audit.AsPlaintext),
)

var StatusPublishedEvent = audit.Declare(audit.EventPolicy[StatusPublished]{
    Semantics: audit.Semantics(1,
        audit.PolicyGolden("status.published.v1", statusPublishedPolicyFingerprint),
    ),
    Descriptor: audit.Descriptor{
        Resource:    "platform.status",
        Action:      "status.published",
        Owner:       "platform.team",
        Purpose:     "business.audit",
        Retention:   "business.forever",
        Consequence: audit.Required,
        Context:     AuditContext,
    },
    Target: audit.EventTarget(
        func(value StatusPublished) audit.Reference { return audit.Reference(value.Slug) },
        audit.Public,
        audit.AsPlaintext,
    ),
    Outcome: audit.EventOutcome(
        audit.Outcomes("published"),
        func(StatusPublished) audit.Outcome { return "published" },
    ),
    OccurredAt: audit.EventOccurredAt(func(value StatusPublished) time.Time { return value.At }),
})

var PublishStatus = audit.DeclareOperation(audit.OperationPolicy{
    Name:        "platform.status.publish",
    Semantics:   audit.Semantics(1),
    Retention:   "business.forever",
    Consequence: audit.Required,
    Context:     AuditContext,
    Members:     audit.OperationMembers(StatusPublishedEvent),
})
```

`PolicyGolden` — версионированный semantic fixture, а не сгенерированный schema
hash, который можно не глядя обновлять. Вычислите и проверьте его через
`ComputeEventFixtureFingerprint` или `ComputeSubjectFixtureFingerprint`, затем
закрепите одобренное значение. Изменение поля, codec, privacy mode, outcome или
extractor без осознанного изменения fixture отвергается.

Для намеренно отсутствующего target используйте `NoEventTarget`. Пустой target
не означает отсутствие. Значения проходят только через `EventValue`,
`EventTokenized`, `EventProtected` или `EventRedacted`; у произвольных map,
ошибок, request bodies, credentials, SQL и локализованного текста нет audit-
представления.

## Сборка и запись

Deployment и runtime разделены. Конструкторы только выделяют память и
валидируют; активацией явно владеет приложение.

```go
catalog, err := audit.Compile(audit.CatalogSpec{
    ID:         "platform.audit",
    Owner:      "platform.team",
    Generation: 1,
    Retention:  audit.RetentionRules(audit.KeepForever("business.forever")),
    Semantics:  semantic.Description(),
    Identities: identities.ActiveDescription(),
    Tokens:     tokens.ActiveDescription(),
    Integrity:  audit.IntegrityOnly(),
}, StatusPublishedEvent, PublishStatus)
if err != nil {
    return err
}

catalogs, err := audit.Lineage(catalog)
if err != nil {
    return err
}
log, err := auditmemory.NewLog(auditmemory.LogSpec{})
if err != nil {
    return err
}
deployment, err := auditmemory.NewDeployment(log)
if err != nil {
    return err
}
change, err := audit.NewCatalogChangeRef("deployments", "release-2026-09")
if err != nil {
    return err
}
if err := deployment.InstallAndActivate(ctx, catalogs, change); err != nil {
    return err
}
store, err := auditmemory.New(auditmemory.Spec{Log: log})
if err != nil {
    return err
}
recorder, err := audit.New(audit.Config{
    Catalogs:   catalogs,
    Writer:     store,
    Context:    applicationAuditContext,
    Semantics:  semantic,
    Identities: identities,
    Tokenizer:  tokens,
})
```

```go
draft, err := StatusPublishedEvent.New(StatusPublished{Slug: "api-v2", At: now})
if err != nil {
    return err
}
result, err := recorder.Record(ctx, draft,
    audit.InOperation(PublishStatus),
    audit.WithIdempotencyKey("publish/api-v2/v1"),
)
if err != nil {
    if key, ok := audit.ReconcileKeyOf(err); ok {
        return reconcileLater(key)
    }
    return err
}
receipt, ok := result.Receipt()
if !ok || receipt.Settlement() != audit.Committed {
    return errUnexpectedSettlement
}
```

Если outcome append неизвестен, не повторяйте бизнес-действие. Сохраните
reconciliation key и вызовите `Recorder.Lookup`. `Recorder.Retry` предназначен
только для самостоятельной записи, про которую доказано, что она не произошла;
transactional uncertainty всегда reconciled, но не replayed.

## Транзакционный CRUD

`auditcrud.Secured` — terminal mutation boundary. При сборке он проверяет model,
обязательные effects, членство в catalog, точный datasource и возможность
открыть root transaction.

```go
base := invoices.Bind(source)
faulted := faults.Enrich[Invoice, int64]()(base.Core)
secured := auditcrud.Secured(recorder, InvoiceAudit, invoicePolicy)(faulted)
repo := crud.Wrap[Invoice, int64, InvoiceUpdate](secured)
```

Порядок: security → audit supervision → faults/store. Поддерживаемая mutation и
её свидетельство вместе commit или rollback. Caller transaction без активной
группы recorder, маскировка через savepoint, другой source, write-only calls,
bulk update/delete и эффекты без доказуемого точного persisted result получают
`audit.ErrUnsupported` или классифицированную transaction error до mutation I/O.
Raw SQL и альтернативные repository handles остаются явными bypass.

## Alpha публичной одностраничной истории

У history отдельная authority. Обычный CRUD-доступ никогда не даёт audit read
access.

```go
historyAuthority := audit.AccessAuthorityFunc(func(
    _ context.Context,
    request audit.AccessRequest,
) (audit.AccessDecision, error) {
    view := request.View()
    var scopes []audit.ScopedReference
    if view.Query.Scope.Kind == audit.ScopeExact {
        scopes = []audit.ScopedReference{view.Query.Scope.Reference}
    }
    return audit.AllowAccess(request, audit.AccessGrantSpec{
        Roles:           []audit.Reference{view.Query.Role},
        Scopes:          scopes,
        Catalogs:        view.Catalogs,
        Resources:       view.Target.Resources,
        Actions:         view.Query.Actions,
        Fields:          view.Query.Fields.Fields,
        Context:         view.Query.Context.Facts,
        Classifications: []audit.Classification{audit.Public},
        Direction:       view.Query.Direction,
        ExpiresAt:       authorityClock.Now().Add(time.Minute),
        MaxRevisions:    view.Query.Limit,
        MaxPages:        1,
        MaxBytes:        1 << 20,
    })
})

history, err := audit.NewHistory(audit.HistoryConfig{
    Profile:  audit.PublicOnePageDevelopmentAlpha,
    Recorder: recorder,
    Log:      store,
    Access:   historyAuthority,
})
if err != nil {
    return err
}

page, err := StatusPublishedEvent.History(history).Events(ctx, audit.Query{
    Purpose:   "incident.review",
    Role:      "auditor",
    Scope:     audit.CurrentScope(),
    Fields:    audit.NoFields(),
    Context:   audit.NoContext(),
    Direction: audit.NewestFirst,
    Limit:     100,
})
```

Authority получает origin-bound `AccessRequest` и обязана ответить через
`AllowAccess(request, grant)` либо `DenyAccess(request, reason)`. Grant может
только сузить нормализованный запрос. Пример зеркалит requested ceiling лишь
для показа полной формы; реальная authority выводит более узкие roles, scopes,
projections, expiry и budgets из политики приложения. Эта alpha выдаёт только `Public` evidence
и одну ограниченную страницу. `HasMore` сообщает о truncation, а `Cursor`
намеренно возвращает `ErrUnsupported`; protected values не выдаются без будущей
предварительно committed access evidence.

Если retained catalog требует подпись, передайте `Verifier` с точным набором
verification keys для всех сохранённых подписанных поколений. `NewHistory`
отклоняет отсутствующий, лишний или несовпадающий inventory, а каждую выдаваемую
revision проверяет по её record-era catalog до projection.

Нулевое значение `Fields` или `Context` означает **none**, а не all. Для явного
полного запроса используйте `AllFields`/`AllContext`, для узкого —
`OnlyFields`/`OnlyContext`.

## Граница интеграций

- Authentication и tenancy отображаются application-owned
  `ContextResolver`: declared facts получают только authenticated principal и
  authority-minted scope.
- Event sourcing записывает ограниченные координаты commit, никогда payload, и
  не заявляет атомарность двух разных stores.
- Jobs разделяют logical effect identity и delivery-attempt identity.
- Storage events могут назвать управляемые namespace/key, но не body, metadata,
  credentials или signed URL.
- OTel observers получают только ограниченные non-identifying outcomes/work
  counts и не являются evidence ledger.
- i18n формирует безопасный текст после классификации; locale и rendered text не
  попадают в evidence.
- Module profiles явно подключают runtime handles. Schema migration, catalog
  activation, workers и destructive work принадлежат lifecycle приложения.

Исполняемые примеры находятся в `test/auditflow`; store-контракт проверяется
через `audit/audittest` и live PostgreSQL integration profile.

## Текущая жёсткая граница

Эту alpha нельзя называть production-complete audit product. Пока отсутствуют
protected disclosure с предварительной access evidence, возобновляемые
immutable cursors, durable attempts, reconstruction/comparison,
correction/dispute, legal-hold projection и purge plans. Capability values
сообщают о них как об unsupported. Актуальный план:
[2026-09-01-audit-log-roadmap.md](../../roadmaps/2026-09-01-audit-log-roadmap.md).
