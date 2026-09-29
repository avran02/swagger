# Аудит goswagger: скрытые баги, недоработки и идеи

Дата: 2026-09-29. Исследованный коммит: `f8186b5`.

Проверены binder, codec, registry, генератор схем, builder, HTTP-хелперы, UI, README и существующие тесты. Исходный код не изменялся. Ниже «воспроизведено» означает запуск отдельного Go-примера против локальной библиотеки; «по коду» — вывод из реализации без интеграционного воспроизведения.

Приоритеты: **P1** — исправить прежде новых фич, поскольку возможны обход проверки или тихая порча данных; **P2** — исправить следующими, поскольку ломается контракт, генерация или отдельный сценарий; **P3** — удобство и документация. Влияние на безопасность конкретного приложения зависит от того, как обработчик использует DTO.

## Статус исправлений после аудита

В текущем рабочем дереве исправлены **B01–B05, B07, B09–B11 и B15**. Добавлены регрессионные тесты. Для B05 также выровнена схема указателя с `required:"true"`: наличие ключа обязательно, `null` разрешён. B06, B08, B12–B14 и остальные наблюдения остаются отдельными задачами; новые фичи не реализованы.

Приоритет выбран по принципу строгого контракта: отклонять неверный вход, а не молча приводить его к удобному виду. Дополнительно запрещены повторяющиеся JSON-ключи, нечисловые NaN/Inf и подстановка Go-имён вместо точных JSON-тегов. Неизвестные JSON-поля отвергаются binder независимо от permissive fallback пользовательского codec. `BindRequest` также явно отклоняет неоднозначные DTO: несколько источников на одном поле, пустое имя внешнего параметра или смешение JSON и multipart body-полей.

Совместимость намеренно ужесточена: запросы с неправильным Content-Type, лишними полями, переполнением и недопустимым null теперь ошибочны. `BindOr422` сохраняет 422 для ошибок связывания, включая Content-Type. Подробные правила добавлены в README. Глобальные ограничения размера тела и полная диагностика конфигурации при старте в этот набор исправлений не входят.

Проверка с race detector, ранее заблокированная, успешно выполнена после переноса Go build cache в `/tmp/swagger-go-cache`. Описания и диагностические скрипты ниже сохраняют исходные наблюдения: после исправлений их результаты должны измениться.

## 1. Баги с высоким приоритетом

### B01 — Хвост после первого JSON-значения отключает required-проверку — P1

**Воспроизведено.** Для ``struct { Name string `json:"name"` }`` тело `{} {}` с `Content-Type: application/json` успешно связывается: `Name == ""`, ошибка `nil`.

Причина: [codec/codec.go:34](codec/codec.go#L34) вызывает `Decoder.Decode` только один раз и не проверяет конец входа. Затем [binder/binder.go:479](binder/binder.go#L479) повторно разбирает всё тело через `json.Unmarshal`; при ошибке просто выходит, не добавляя ошибку валидации. В результате второй JSON-объект или мусор после первого скрывают отсутствие обязательных полей.

**Исправление:** после первого значения требовать EOF; ошибку разбора для валидации нельзя игнорировать. Лучше единый разбор с проверкой присутствия полей. Регрессионные случаи: `{} {}`, `{} garbage`, корректный объект с пробелами после него.

### B02 — Смена Content-Type позволяет пропустить обязательное JSON-тело — P1

**Воспроизведено.** Тот же DTO принимает `{}` с `text/plain` без ошибки. Пустой multipart (`--x--\r\n`, boundary `x`) тоже принимается, хотя DTO содержит обязательное JSON-поле.

В [binder/binder.go:299](binder/binder.go#L299) multipart выбирается только по заголовку, даже для DTO без form-полей. Для другого непустого тела неподходящий Content-Type приводит к `return nil` до проверки required. Проверка `strings.Contains(ct, "application/json")` дополнительно допускает посторонние media type, содержащие эту подстроку.

**Исправление:** явно сопоставлять допустимый формат DTO и разобранный media type; возвращать ошибку неподдерживаемого формата, которую HTTP-хелпер может превратить в 415. Проверять обязательность тела независимо от выбора декодера. Добавить случаи без Content-Type, с `text/plain`, multipart для JSON DTO и валидным JSON с charset.

### B03 — JSON заполняет поля, предназначенные для заголовков/query/path — P1

**Воспроизведено.** Для DTO:

```go
type Request struct {
    Name   string `json:"name"`
    Tenant int    `header:"X-Tenant-ID" required:"false"`
}
```

Тело `{"name":"ok","Tenant":123}` без заголовка успешно заполняет `Tenant=123`. `DisallowUnknownFields` это не предотвращает: декодируется весь DTO, и экспортированное поле без `json`-тега известно стандартному JSON-декодеру. Если внешнее поле необязательно и его источник отсутствует, [bindNonBody](binder/binder.go#L381) оставляет значение из тела.

**Риск:** обработчик может ошибочно считать, что значение пришло из заголовка, выставленного middleware или прокси. Сам по себе пример не доказывает обход авторизации в приложении.

**Исправление:** JSON должен иметь доступ только к полям своего источника. До этого явно ставить `json:"-"` на поля других источников. Добавить тесты на подстановку через имя Go-поля, разный регистр и отсутствие внешнего параметра. Место: [binder/binder.go:329](binder/binder.go#L329).

### B04 — Числа обрезаются до размера поля без ошибки — P1

**Воспроизведено.** ``N uint8 `query:"n"` ``, запрос `?n=256`: результат `N=0`, ошибка `nil`.

В [binder/binder.go:214](binder/binder.go#L214) все integer/float значения парсятся как 64-битные, затем `reflect.SetInt/SetUint/SetFloat` записывает их в поле фактического размера. Аналогично страдают `int8/int16/int32`, `uint16/uint32` и диапазон `float32`.

**Исправление:** парсить с `t.Bits()`; отдельно определить политику для NaN/Inf. Проверить границы каждого поддерживаемого числового типа. Это особенно важно для лимитов, идентификаторов и небольших числовых enum.

## 2. Ошибки валидации и согласованности DTO

### B05 — Непоследовательная обработка null и вложенных коллекций — P2

**Воспроизведено**, [binder/binder.go:487](binder/binder.go#L487):

- `{"name":null}` принимается для обязательного `Name string`, оставляя пустую строку; схема не объявляет это поле nullable.
- `{"child":null,"items":[]}` отвергается для необязательного `Child *Child`, если внутри `Child` есть обязательное `Name`: ошибка `body.child.name: Field required`. При этом схема разрешает nullable-указатель.
- `{"items":[{}]}` принимается для `Items []Child`, хотя у элемента отсутствует обязательное поле. Валидация спускается в struct, но не в slice/map.

**Исправление:** разделить отсутствие ключа и `null`, учитывать nullable до рекурсии, обходить элементы коллекций. Проверить также `required:"true"` у указателя: runtime учитывает его, а required-список JSON-схемы исключает указатели безусловно ([schema/schema.go:246](schema/schema.go#L246)).

### B06 — Встроенный указатель документируется, но не связывается — P2

**Воспроизведено.** ``type Base struct { ID int `query:"id"` }; type Request struct { *Base }``. Запрос `?id=42` возвращает успех, `Base` остаётся `nil`.

[compileFields](binder/binder.go#L154) обходит только embedded struct по значению, а [collectParameters](schema/schema.go#L390) также обходит указатели. Для JSON аналогичная асимметрия есть в поиске body-полей. Клиент видит параметр в контракте, но сервер его игнорирует.

**Исправление:** общая логика обхода embedded-полей; выделение памяти для nil-указателей при связывании; защита от циклов. Проверять один и тот же DTO одновременно через binder и генератор.

### B07 — Тег исключения превращается в обязательный параметр с именем «-» — P2

**Воспроизведено.** Поле ``X int `query:"-"` ``, пустой query → `query.-: Field required`. Генератор такое поле исключает.

[compileFields](binder/binder.go#L161) не отфильтровывает `"-"` и пустое имя, в отличие от [fieldParameters](schema/schema.go#L427). `form:"-"` дополнительно включает multipart-режим. Нужно нормализовать и фильтровать теги до формирования плана.

### B08 — Типы и аннотации параметров теряются в схеме — P2

**Воспроизведено.** ``N int `query:"n" default:"5" enum:"1,5" format:"int32"` `` даёт строковый default `"5"`, пустой enum и формат `int64`.

[fieldParameters](schema/schema.go#L420) использует `buildSchema`, минуя обработку `enum` и `format`. В [fieldSchema](schema/schema.go#L307) numeric/bool `default`, `example`, `enum` также сохраняются строками. Multipart-схема тоже не применяет общую обработку аннотаций.

**Последствие:** схема не отражает DTO-теги; примеры и defaults имеют другой тип, чем объявленное поле. **Исправление:** единый обработчик метаданных с приведением к типу поля и диагностикой некорректного тега. Не смешивать документирование enum с фактической runtime-валидацией: сейчас binder enum не проверяет.

## 3. Registry и генерация контракта

### B09 — После ошибки Walk кеш считается успешно заполненным — P2

**Воспроизведено.** Walker сообщает один маршрут и возвращает ошибку. Первый `Endpoints()` возвращает ошибку; второй возвращает один endpoint без ошибки, не вызывая walker повторно.

В [registry/registry.go:117](registry/registry.go#L117) результат дописывается прямо в `r.resolved` до проверки `walkErr`; следующий вызов принимает ненулевой slice за готовый кеш.

**Исправление:** собирать snapshot локально и публиковать только после полного успеха. Тестировать неуспех после первого маршрута, повторный вызов и успешное восстановление.

### B10 — Незарегистрированный value-handler вызывает panic при обходе — P2

**Воспроизведено.** Если walker передаёт обычный struct с методом `ServeHTTP`, [handlerPtr](registry/registry.go#L160) вызывает `reflect.Value.Pointer()` для `struct` и паникует. Это происходит даже для маршрута, который библиотека не должна документировать.

**Исправление:** проверять kind/поддерживаемый тип handler и безопасно пропускать неизвестные обработчики; не рассчитывать, что любой `http.Handler` имеет указатель. Добавить value-handler, pointer-handler, middleware wrapper и nil в тесты.

### B11 — Возвращаемый snapshot позволяет изменить внутренний кеш — P2

**Воспроизведено.** После `eps, _ := reg.Endpoints(); eps[0].Path = "/changed"` следующий `Endpoints()` возвращает `/changed`.

[registry/registry.go:100](registry/registry.go#L100) отдаёт внутренний slice. Mutex защищает получение, но не последующие внешние записи. Вложенные slices/maps метаданных также копируются поверхностно.

**Исправление:** возвращать независимый snapshot и копировать изменяемые поля при регистрации. Изменение полученных метаданных не должно менять результат следующей генерации; параллельное использование такого snapshot стоит проверить с race detector.

### B12 — Разные Go-типы сливаются в один component — P2

**Воспроизведено для anonymous struct.** Два типа ``struct { A string `json:"a"` }`` и ``struct { B int `json:"b"` }`` получают одинаковый `$ref: #/components/schemas/` и единственный component с пустым именем.

[typeName](schema/schema.go#L481) возвращает пустую строку для anonymous struct, а для именованного типа использует только последний сегмент пути пакета. Поэтому, **по коду**, два разных пакета `.../v1/dto.User` и `.../v2/dto.User` тоже столкнутся. [structSchema](schema/schema.go#L198) принимает совпадение имени за уже построенный тип.

**Исправление:** идентифицировать типы по `reflect.Type`, выдавать отдельные стабильные имена; anonymous struct оставлять inline либо именовать по стабильному идентификатору. Проверить одинаковые имена из разных пакетов и generic DTO.

### B13 — Нормализация пути ломает regexp с фигурными скобками — P2

**Воспроизведено через переданный walker.** Маршрут `/users/{id:[0-9]{2}}` превращается в `/users/{id}}`, а operationId — в `getUsersById}`.

[reChiParam](builder/builder.go#L326) заканчивает совпадение на первой `}`, включая закрывающую скобку квантификатора. **Исправление:** разбирать границы параметров с учётом regexp/вложенных скобок, а не одним таким выражением. Проверить `{2}`, `{2,4}`, экранирование и несколько параметров. Интеграция с реальным chi в этом аудите не запускалась.

### B14 — Разным маршрутам присваивается одинаковый operationId — P2

**Воспроизведено.** `GET /a-b` и `GET /a_b` оба получают `getAB`; аналогично могут сталкиваться границы сегментов.

[operationID](builder/builder.go#L333) теряет разделители; уникальность результата не проверяется. Это делает имена операций неоднозначными, особенно для генераторов клиентов.

**Исправление:** добавить явный `WithOperationID` и проверять уникальность на финальном документе. Автоматические имена при коллизии либо стабильно дополнять, либо возвращать понятную ошибку.

## 4. UI и дополнительные недоработки

### B15 — Самописное экранирование JavaScript допускает выход из script — P2, условный риск

**Воспроизведено на HTML-ответе.** `UIConfig{SpecURL: "</script><script>alert(1)</script>"}` попадает в страницу буквально. [jsonString](ui.go#L234) экранирует лишь кавычки и обратный слеш, затем [js](ui.go#L232) маркирует результат доверенным JavaScript. Переводы строк также не кодируются как JSON.

Обычный HTTP-посетитель не управляет `UIConfig` в текущем коде. Поэтому это **не доказанная удалённая XSS** при стандартной статической конфигурации, а опасное место при подстановке внешней конфигурации и ошибка сериализации. Браузерный exploit отдельно не запускался.

**Исправление:** использовать стандартное JSON-кодирование с HTML-экранированием либо безопасную передачу значений в шаблон без ручной маркировки. Проверить `</script>`, кавычки, переводы строк и Unicode.

### Наблюдения по коду, требующие отдельных задач

1. **Неограниченное чтение тела:** [binder/binder.go:325](binder/binder.go#L325) использует `io.ReadAll` без собственного лимита. Лимит multipart `32 << 20` задаёт порог памяти, а не общий размер загрузки. Добавить настраиваемый максимальный размер и пример ограничения на уровне HTTP-handler; влияние зависит от внешних лимитов приложения/прокси.
2. **Редирект docs за reverse proxy:** [ui.go:111](ui.go#L111) выдаёт `/docs/` при `prefix="/docs"`, даже если внешний URL `/service/docs`, а прокси удаляет `/service`. `Config.BasePath` этого не меняет. Нужен редирект, сохраняющий внешний префикс, и интеграционный тест.
3. **Успешные ответы ограничены 200/204:** [builder/builder.go:224](builder/builder.go#L224). Нет полноценного описания 201, 202, нескольких успешных исходов. `WithResponseHeader` теряется на ветке 204 без response DTO. Это мешает точно описать создание ресурса и асинхронную обработку.
4. **JSON-структура и схема могут расходиться:** [schema/schema.go:198](schema/schema.go#L198) игнорирует экспортированные поля без `json`-тега, хотя JSON-кодек их сериализует; embedded struct разворачивается до учёта его `json:"name"`/`json:"-"`. `structSchemaInline` не обходит многоуровневые embeds полноценно. Нужны тесты сравнения фактического JSON со схемой.
5. **Неполная поддержка типов:** `[N]T` попадает в fallback `string`; `map[string]*T` теряет nullable значения, поскольку использует `buildSchema`, а не `SchemaForType`; пользовательские JSON-marshaler типы не имеют механизма задания собственной схемы.
6. **Required multipart расходится:** binder по умолчанию считает slice необязательным ([binder/binder.go:465](binder/binder.go#L465)), а schema — обязательным ([schema/schema.go:459](schema/schema.go#L459)). Для `[]*multipart.FileHeader` без required-тега получится разный контракт и поведение.
7. **Нет диагностики пропавших маршрутов:** [builder/builder.go:170](builder/builder.go#L170) молча пропускает регистрации без path/method. Отсутствующий walker или несовпадение handler после обёртки выглядит как успешно сгенерированная неполная спецификация.
8. **Не задан извлекатель path-параметров:** [binder/binder.go:382](binder/binder.go#L382) молча пропускает required-проверку path, если `SetURLParamFunc` не вызван. Лучше ошибка конфигурации или явно выбранный адаптер роутера.
9. **Известная коллизия handler code pointer:** README уже предупреждает о closures и методах разных экземпляров одного типа. Она остаётся архитектурной проблемой: [registry/registry.go:68](registry/registry.go#L68) без предупреждения заменяет DTO/security предыдущей регистрации. Это не новая находка аудита. Простая обёртка новой closure проблему не решает: её code pointer тоже может совпадать.
10. **README отстал от binder:** раздел Binder limitations утверждает, что отсутствующее и пустое значение не различаются, но текущий binder различает `present` и `raw`: `?q=` для string остаётся пустым, числовое пустое значение вызывает ошибку. Комментарий к UI CDN говорит `@latest`, а константа закреплена на `5.17.14`.

## 5. Идеи фич

Это предложения по развитию текущего проекта, без оценки внешних продуктов. Оценки объёма относительные: S — локальная доработка, M — несколько модулей, L — архитектурная задача.

| Фича | Что получает пользователь | Минимальная полезная версия | Объём / порядок |
|---|---|---|---|
| **«Доктор контракта»** | Понимает на старте, почему endpoint пропал или схема расходится с DTO | `Validate`/`BuildStrict`: непривязанные регистрации, коллизии имён и operationId, неподдерживаемые типы, невалидные defaults; сообщения с маршрутом и полем | M / первой после P1 |
| **Проверка изменений API в CI** | Видит, что релиз сломает существующих клиентов | Сравнение сохранённого и нового OpenAPI: удалённые пути, новые required-поля, изменения типов; Markdown-отчёт и ненулевой exit code для breaking changes | M / затем |
| **Наблюдатель за ответами в dev/test** | Узнаёт, когда реальный HTTP-ответ не соответствует документации | Middleware сверяет status и Content-Type; позже — JSON-схему. Логирует отклонения, не сохраняет тела по умолчанию; streaming/hijacking явно учитывать | L / затем |
| **Полноценные успешные ответы** | Документирует 201 Created, 202 Accepted, 204 и Location | Общий `WithResponseStatus`/`WithResponseFor`, заголовки для любого статуса, несколько content types | M / ранняя |
| **Расширяемый binder и schema adapters** | Связывает time, UUID, enum и прикладные типы одинаково в runtime и документации | Реестр преобразователей типа, поддержка `encoding.TextUnmarshaler`, повторяющиеся query-параметры, общий источник метаданных | L / после унификации DTO |
| **Изолированный экземпляр API** | Несколько независимых API/роутеров без глобального состояния и коллизий регистрации | Объект, владеющий registry/config/codec/извлекателем; явная идентичность регистрации или `(method,path)`; прежние функции как фасад default instance | L / стратегическая |
| **Сценарии и mock-ответы из DTO** | Получает исполняемые примеры API до готовности backend | Валидные примеры из tags/required, cURL и `.http`-файл; детерминированный mock handler для тестов фронта с ограничением глубины рекурсии | M–L / после исправления схем |
| **Docs без интернета** | Открывает документацию в закрытом контуре | Локальные закреплённые UI-assets через `embed.FS`, настройка CSP и хранения авторизации; проверка работы без CDN | M / независимая |
| **Кеш готовой спецификации + ETag** | Быстро отдаёт неизменную документацию, поддерживает 304 | Кеш JSON/YAML по версии registry и config, инвалидируемый при изменении регистраций | M / после исправления кеша registry |

Наиболее сильная связка для проекта: **общие правила DTO → доктор контракта → проверка breaking changes → проверка реальных ответов**. Она развивает основную идею библиотеки — меньше ручного рассогласования между обработчиком и документацией.

## 6. Рекомендуемая последовательность

1. Закрыть B01–B04 и добавить регрессионные тесты на каждый обход/переполнение.
2. Унифицировать обход DTO, источники значений, required/null и типизацию аннотаций: B05–B08, multipart и embeds.
3. Исправить snapshot registry и идентичность схем: B09–B12; отдельно спроектировать устранение code-pointer коллизий.
4. Исправить пути, operationId и JS-кодирование: B13–B15. Добавить итоговую валидацию сгенерированного документа.
5. Выпустить первую полезную фичу: «доктор контракта» и нормальное описание успешных ответов. Затем переходить к CI diff и runtime-наблюдателю.

## 7. Что проверено и что осталось

- `go test ./...` — успешно для всего модуля.
- `go vet ./...` — успешно.
- Отдельные Go-примеры подтвердили B01–B15. Для B05 проверены три описанных JSON-сценария; для B12 — anonymous struct, коллизия имён пакетов установлена по коду; B15 проверен по сформированному HTML.
- `go test -race ./...` — не завершился успешно: ошибка сборки `cannot find package` для `example/advanced` и `runtime/race`, затем build failures. Причина не установлена; отсутствие гонок не подтверждено, эту ошибку нельзя считать доказательством дефекта библиотеки.
- Не выполнялись интеграционные проверки с chi, reverse proxy, браузером, внешним OpenAPI-валидатором или генераторами SDK. Сценарии с роутами проверялись через предусмотренный библиотекой walker.
- Падение от неограниченной рекурсии, нагрузочные атаки и эксплуатация приложений не запускались. Отчёт относится к библиотеке, а не доказывает наличие уязвимостей у её потребителей.

## Приложение: исполняемый пример основных находок

Скопировать следующий блок в `/tmp/swagger-audit.go` и из корня проекта выполнить `go run /tmp/swagger-audit.go`. Пример печатает наблюдаемое поведение; это диагностический скрипт, не тест с ожидаемым падением после исправления.

```go
package main

import (
	"errors"
	"fmt"
	swagger "github.com/avran02/swagger"
	"github.com/avran02/swagger/binder"
	"github.com/avran02/swagger/builder"
	"github.com/avran02/swagger/registry"
	"github.com/avran02/swagger/schema"
	"github.com/avran02/swagger/types"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
)

type Body struct {
	Name string `json:"name"`
}
type Child struct {
	Name string `json:"name"`
}
type Nested struct {
	Child *Child  `json:"child"`
	Items []Child `json:"items"`
}
type Params struct {
	N uint8 `query:"n"`
}
type Base struct {
	ID int `query:"id"`
}
type Embed struct{ *Base }

func handler(http.ResponseWriter, *http.Request) {}
func body(label, data, ct string, dst any) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(data))
	r.Header.Set("Content-Type", ct)
	err := binder.BindRequest(r, dst)
	fmt.Printf("%s: value=%+v err=%v\n", label, dst, err)
}
func main() {
	body("trailing JSON bypasses required", `{} {}`, "application/json", &Body{})
	body("wrong content type", `{}`, "text/plain", &Body{})
	body("multipart bypasses JSON", "--x--\r\n", "multipart/form-data; boundary=x", &Body{})
	body("null scalar", `{"name":null}`, "application/json", &Body{})
	body("null optional object", `{"child":null,"items":[]}`, "application/json", &Nested{})
	body("required field in array", `{"items":[{}]}`, "application/json", &Nested{})
	p := Params{}
	err := binder.BindRequest(httptest.NewRequest("GET", "/?n=256", nil), &p)
	fmt.Printf("uint8 overflow: %+v err=%v\n", p, err)
	e := Embed{}
	err = binder.BindRequest(httptest.NewRequest("GET", "/?id=42", nil), &e)
	fmt.Printf("embedded pointer: %+v err=%v\n", e, err)
	var skip struct {
		X int `query:"-"`
	}
	err = binder.BindRequest(httptest.NewRequest("GET", "/", nil), &skip)
	fmt.Printf("ignored query tag: err=%v\n", err)
	g := schema.NewGenerator()
	a := g.SchemaForType(reflect.TypeOf(struct {
		A string `json:"a"`
	}{}))
	b := g.SchemaForType(reflect.TypeOf(struct {
		B int `json:"b"`
	}{}))
	fmt.Printf("anonymous schemas: %q %q components=%v\n", a.Ref, b.Ref, g.Components)
	g = schema.NewGenerator()
	ps := g.ParametersForType(reflect.TypeOf(struct {
		N int `query:"n" default:"5" enum:"1,5" format:"int32"`
	}{}))
	fmt.Printf("parameter: default=%#v enum=%v format=%s\n", ps[0].Schema.Default, ps[0].Schema.Enum, ps[0].Schema.Format)
	reg := registry.NewRegistry()
	reg.RegisterMeta(handler, types.EndpointMeta{})
	calls := 0
	reg.SetWalker(func(fn registry.WalkFunc) error {
		calls++
		fn("GET", "/ok", http.HandlerFunc(handler))
		return errors.New("walk failed")
	})
	_, err = reg.Endpoints()
	fmt.Printf("walker first: err=%v\n", err)
	eps, err := reg.Endpoints()
	fmt.Printf("walker second: len=%d err=%v calls=%d\n", len(eps), err, calls)
	reg = registry.NewRegistry()
	reg.RegisterMeta(handler, types.EndpointMeta{})
	reg.SetWalker(func(fn registry.WalkFunc) error {
		for _, p := range []string{"/users/{id:[0-9]{2}}", "/a-b", "/a_b"} {
			fn("GET", p, http.HandlerFunc(handler))
		}
		return nil
	})
	sp, err := builder.Build(reg, builder.DefaultConfig())
	for p, v := range sp.Paths {
		fmt.Printf("path=%s operationId=%s\n", p, v.Get.OperationID)
	}
	w := httptest.NewRecorder()
	swagger.ServeUI(swagger.UIConfig{SpecURL: "</script><script>alert(1)</script>"})(w, httptest.NewRequest("GET", "/docs/", nil))
	fmt.Printf("UI raw script injection=%v\n", strings.Contains(w.Body.String(), "</script><script>alert(1)</script>"))
}
```

Дополнительный пример B03, B10 и B11 (отдельный Go-файл):

```go
package main

import (
	"fmt"
	"github.com/avran02/swagger/binder"
	"github.com/avran02/swagger/registry"
	"github.com/avran02/swagger/schema"
	"github.com/avran02/swagger/types"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
)

type valueHandler struct{}

func (valueHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}
func h(http.ResponseWriter, *http.Request)                        {}
func main() {
	var dst struct {
		Name   string `json:"name"`
		Tenant int    `header:"X-Tenant-ID" required:"false"`
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"ok","Tenant":123}`))
	r.Header.Set("Content-Type", "application/json")
	err := binder.BindRequest(r, &dst)
	fmt.Printf("header injected through JSON: %+v err=%v\n", dst, err)
	reg := registry.NewRegistry()
	reg.RegisterMeta(h, types.EndpointMeta{})
	reg.SetWalker(func(fn registry.WalkFunc) error { return fn("GET", "/real", http.HandlerFunc(h)) })
	eps, _ := reg.Endpoints()
	eps[0].Path = "/changed"
	eps, _ = reg.Endpoints()
	fmt.Printf("modified snapshot: path=%s\n", eps[0].Path)
	g := schema.NewGenerator()
	ty := reflect.TypeOf(struct {
		Files []*http.Request `form:"files"`
	}{})
	fmt.Printf("slice form required schema=%v\n", g.MultipartSchemaForType(ty).Required)
	defer func() { fmt.Printf("value handler panic: %v\n", recover()) }()
	reg.SetWalker(func(fn registry.WalkFunc) error { return fn("GET", "/unregistered", valueHandler{}) })
	reg.Endpoints()
}
```
