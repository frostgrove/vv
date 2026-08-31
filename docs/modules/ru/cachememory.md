# cache/cachememory

`cachememory` — ограниченный in-process backend для `cache`. Это storage kind по
умолчанию для профиля `Hot`; он реализует `cache.Backend` и
`cache.BatchReader`, не добавляя сторонних зависимостей.

```go
backend, err := cachememory.New(cachememory.Limits{
	MaxEntries:   10_000,
	MaxBytes:     256 << 20,
	MaxItemBytes: 16 << 20,
})
```

Все три лимита обязательны. Entry учитывается по публичной версионированной
модели: `FixedEntryChargeBytes + len(value)`. `EntryCharge` позволяет проверять
конфигурацию тем же расчётом. Значение копируется при записи и чтении, поэтому
caller не может изменить сохранённые байты через оставленный slice.

Backend поддерживает строгий LRU живых entries. Успешное чтение поднимает entry;
expired entries удаляются до вытеснения живых. Не помещающийся put отклоняется
до мутации. Batch read дедуплицирует адреса, соблюдает item/total limits и
возвращает собственные byte slices. `Reset` очищает storage, `Close` очищает его
навсегда, `Stats` показывает entries, charged bytes, limits и closed state.

`WithClock` нужен для детерминированных expiry-тестов. `WithObserver` сообщает
операции storage, причины eviction и charges. Callbacks выполняются вне lock,
panic изолируется, re-entry для этого backend безопасен. Cancellation
проверяется во время ограниченных scans и планирования eviction. Отклонённый
или отменённый put не публикует запрошенное значение; уже завершившиеся expiry
cleanup или LRU promotion могут остаться видимыми, если cancellation победил
позже.

Это локальное для процесса disposable storage. Оно не координирует loaders, не
обеспечивает persistence, replication или distributed lock — это обязанности
типизированного facade либо другого backend.
