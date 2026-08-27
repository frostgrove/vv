# vvcfg — структура конфигурации, загружаемая и валидируемая при старте приложения

```go
import "github.com/frostgrove/vv/utils/vvcfg"
```

```bash
go get github.com/frostgrove/vv/utils/vvcfg
```

**Модуль:** отдельный — cleanenv является зависимостью, а корневой модуль не
берёт ни одной ([[D-033]], [[D-036]]) · **Требует:** `github.com/ilyakaznacheev/cleanenv`

Декодирование — целиком дело cleanenv: YAML, TOML, JSON, `.env` и теги
окружения. Этот модуль добавляет три вещи, которые реально нужны приложению
вокруг этого: **заявленный приоритет** поиска файла, **возврат ошибки** вместо
паники и **хук валидации**, который выполняется раньше всего остального.

---

## Использование

```go
type Config struct {
    Addr string `yaml:"addr" env:"ADDR" env-default:":8080"`
    DSN  string `yaml:"dsn"  env:"DSN"`
}

func (c *Config) Validate() error {
    if c.DSN == "" {
        return errors.New("dsn is required")
    }
    return nil
}

func main() {
    cfg := vvcfg.MustLoad[Config]()
    …
}
```

| | |
|---|---|
| `Load[T](path)` | декодирует, затем вызывает `Validate()`, если у структуры он есть |
| `MustLoad[T](path...)` | загружает и паникует при ошибке; объединяет явные сегменты через `filepath.Join` |

Без явно переданного пути `MustLoad` сам определяет файл.

## Хук валидации — в этом весь смысл

```go
type Validator interface{ Validate() error }
```

`Load` вызывает его после декодирования и возвращает то, что вернул он.

**Неверная конфигурация должна останавливать процесс при старте** — а не
проявляться запутанным сбоем, когда уже пошёл трафик ([[D-021]]). Это то же
правило, которому следует `sqlrepo.Define` для сломанного маппинга модели и
`probe.Full` для неизвестной таблицы.

## Приоритет

```
MustLoad("config", "app.yml")  явный путь, соединённый через filepath.Join
--config-path <path>            флаг для переопределения при развёртывании из командной строки
CONFIG_PATH=<path>              переменная окружения
DefaultCfgPath                  по умолчанию `./config/app.yml`
DefaultCfgPath = ""             конфигурация только из environment
```

Присвой `vvcfg.DefaultCfgPath` другое значение до старта приложения, если
проекту нужен иной путь по умолчанию.

## Смотрите также

- [vvflag](vvflag.md) — как читается `--config-path`, не завладевая командной строкой
