# Go Voice MVP

Полноценное приложение голосовой связи на Go. Цель проекта — современная
платформа с качественными голосовыми каналами и новыми сценариями совместной
работы, а не упрощённая копия TeamSpeak.

Подробная продуктовая дорожная карта находится в
[`go-voice-roadmap.md`](go-voice-roadmap.md). Этот README описывает фактическое
состояние кода и порядок ближайших технических исправлений.

## Текущая архитектура

Клиент и сервер используют один UDP transport на порту `9000`. Через него идут
как control-пакеты, так и Opus voice-пакеты.

```text
microphone
    -> PCM frames
    -> Opus encoder
    -> VoicePacket
    -> ClientPacketConn -> DatagramCodec -> UDP
    -> UDP -> ServerPacketConn -> DatagramCodec
    -> участники того же канала
    -> Opus decoder
    -> audio player
```

Сервер не декодирует звук: `VoicePacket.Payload` остаётся непрозрачным набором
байтов и пересылается другим клиентам.

### Уже реализовано

- UDP handshake `Hello` / `HelloAck`;
- server-side sessions с криптографически случайным `SessionID`;
- самостоятельная доменная модель каналов со стабильным в пределах запуска
  `ChannelID`, иерархией, метаданными и фиксированным Opus-профилем;
- потокобезопасный server-side registry каналов, участников и
  `StateRevision`;
- heartbeat каждые 5 секунд;
- удаление session после 30 секунд неактивности;
- подключение и переключение канала через `JoinChannel`;
- разделение входящих voice и control packets;
- Opus encode/decode;
- захват микрофона через malgo;
- воспроизведение через Oto;
- `RequestID`, ожидание ответа и повтор control-запроса;
- server-side deduplication через `RequestCache`;
- явный disconnect клиента;
- единая codec-граница для всех логических пакетов с явным направлением,
  endpoint и отдельным контекстом получателя (`KeyOwnerID`);
- разные transport-типы для connected client socket и unconnected server
  socket;
- безопасный drop повреждённых/rejected датаграмм без остановки receive loop.

### Известные ограничения

- jitter buffer использует фиксированное окно; ещё нет Opus PLC, адаптивной задержки
  и индивидуальной регулировки громкости участников;
- handshake deduplication требует стабильного `IP:port` на время retry;
- входящие voice-пакеты пока декодируются ещё до завершения join;
- нет аутентификации, шифрования и reconnect.
- сервер пока создаёт только постоянный канал `default`; сетевого API создания
  каналов, persistence и синхронизации полного состояния с клиентом ещё нет.

## Структура проекта

```text
cmd/server/              запуск UDP-сервера
cmd/client2/             запуск голосового клиента
internal/audio/          устройства, PCM и Opus
internal/client/         состояние и goroutine клиента
internal/domain/         общие модели каналов, участников и ревизии
internal/protocol/       бинарный формат пакета
internal/server/         фоновые процессы сервера
internal/transport/udp/  чтение и запись UDP
internal/voice/          sessions, channels, routing и request cache
```

Пакеты внутри `internal` доступны только коду этого Go-модуля. Это позволяет
менять внутреннюю архитектуру, не создавая преждевременный публичный API.

## Запуск

Требуется Go `1.27.1` и доступные системные audio devices.

Запустить сервер:

```bash
go run ./cmd/server
```

Запустить клиентов в отдельных терминалах:

```bash
go run ./cmd/client2 -name alice -channel default
go run ./cmd/client2 -name bob   -channel default
```

Команды клиента:

```text
/join default
/quit
```

Проверки проекта:

```bash
go test ./...
go vet ./...
go test -race ./...
```

На Windows для `go test -race` нужен GCC с `mingw-w64` runtime 8 или новее.
Проверить установленный compiler можно так:

```powershell
gcc --print-file-name libsynchronization.a
```

Команда должна вывести полный путь к существующему файлу, а не только имя
`libsynchronization.a`. В текущем окружении используется WinLibs POSIX/UCRT,
а Go настроен командами `go env -w CC=gcc CXX=g++`.

## Бинарный UDP-протокол

Каждый datagram содержит 17-байтный заголовок и payload:

```text
offset   field       type     size
0        Type        uint8    1 byte
1..8     SessionID   uint64   8 bytes, Big Endian
9..12    Sequence    uint32   4 bytes, Big Endian
13..16   RequestID   uint32   4 bytes, Big Endian
17..N    Payload     []byte   оставшиеся bytes
```

Типы пакетов:

```text
1  Hello
2  Voice
3  HelloAck
4  Heartbeat
5  Disconnect
6  JoinChannel
7  JoinChannelAck
8  Error
```

`Sequence` задаёт порядок voice-пакетов. `RequestID` связывает control request
с response и остаётся одинаковым для всех retry одной логической операции. До
создания session handshake использует ключ `(IP:port, RequestID)`.
Transport принимает максимум `MaxWireDatagramSize == 1217` байт: 17 байт
заголовка и до 1200 байт payload. Этот предел принадлежит протоколу и не может
быть увеличен реализацией codec. Буфер чтения имеет дополнительный байт,
позволяющий обнаружить и отклонить слишком большой UDP datagram.

Все пакеты проходят через `DatagramCodec`. Его `DatagramContext` отдельно
передаёт направление, endpoint и `KeyOwnerID`. Поэтому при серверной пересылке
`VoicePacket.SessionID` продолжает обозначать говорящего, а `KeyOwnerID` —
конкретного получателя. Некорректный вход классифицируется как
`ErrRejectedDatagram` и тихо отбрасывается; ошибки socket и внутренние ошибки
transport остаются фатальными.

Текущие и целевые параметры протокола:

| Параметр | Значение | Состояние |
|---|---:|---|
| UDP read buffer | 1218 bytes | дополнительный байт обнаруживает превышение |
| Максимальный datagram | 1217 bytes | реализовано |
| Максимальный payload | 1200 bytes | реализовано |
| Handshake timeout | 3 секунды на попытку | реализовано |
| Control request timeout | 3 секунды на попытку | реализовано |
| Control request attempts | 3 | реализовано |
| Heartbeat interval | 5 секунд | реализовано |
| Session timeout | 30 секунд | реализовано |
| Session cleanup interval | 5 секунд | реализовано |

## Завершённые технические этапы

Ниже сохранён список уже выполненных крупных исправлений. Актуальный порядок
дальнейшей разработки находится в
[`readme_docs/development-plan.md`](readme_docs/development-plan.md), а
отложенные направления — в [`readme_docs/backlog.md`](readme_docs/backlog.md).

### Этап 0. Привести документацию и окружение в актуальное состояние

- [x] Обновить этот README под текущую реализацию: UDP `:9000`, команды
  `cmd/server` и `cmd/client2`, бинарный заголовок размером 17 байт.
- [x] Удалить устаревшее описание TCP/JSON control plane и команд `/say`,
  `/burst`.
- [x] Зафиксировать ограничения протокола: максимальный payload, допустимые
  типы пакетов, timeout и количество повторных запросов.
- [x] Исправить локальный `GOROOT`: он должен указывать на корень Go SDK, а не
  на каталог `bin`.
- [x] Добавить корневой `.gitignore` для `.idea`, `*.iml`, `*.exe`, архивов и
  других локальных артефактов.
- [x] Удалить либо документировать экспериментальный `cmd/main.go`.

Критерий готовности: команды запуска и описание wire format в README совпадают
с кодом; `go test ./...` и `go vet ./...` работают без временной настройки
окружения.

### Этап 1. Исправить присоединение к каналу

- [x] Сделать `client.JoinChannel` возвращающим реальную ошибку из `DoRequest`,
  не скрывать её внутри `handleJoin`.
- [x] Не запускать отправку аудио до получения `JoinChannelAck`.
- [x] На сервере отклонять voice-пакеты от сессии с пустым channel.
- [x] Проверять, что payload в `JoinChannelAck` соответствует запрошенному
  каналу.
- [x] Добавить тесты на успешный join, server error, timeout и потерянный ACK.

Критерий готовности: клиент не передаёт голос до подтверждённого join и
завершается с понятной ошибкой, если присоединение не удалось.

### Этап 2. Ограничить протокол и сделать control-запросы надёжными

- [x] Ввести общие константы `MaxWireDatagramSize` и `MaxPayloadSize` в пакете
  `protocol` (`MaxDatagramSize` временно сохранён как совместимый alias).
- [x] Проверять размер при кодировании, чтении и декодировании пакета.
- [x] Обнаруживать усечённые UDP datagram вместо передачи повреждённого payload
  в Opus decoder.
- [x] Добавить `RequestID` или отдельный nonce в handshake.
- [x] Сделать повторный `Hello` идемпотентным, чтобы потерянный `HelloAck` не
  создавал новую session.
- [x] Добавить TTL и максимальный размер `RequestCache`.
- [x] Удалять cache entries при disconnect и session timeout.

Критерий готовности: повтор одного запроса не выполняет операцию второй раз,
cache имеет ограниченный размер, слишком большие и усечённые пакеты
отклоняются.

### Этап 3. Исправить многопользовательское аудио

- [x] Добавить `SenderID` и `Sequence` в принимаемый media frame.
- [x] Создавать отдельный Opus decoder для каждого удалённого пользователя.
- [x] Добавить per-user jitter buffer с переупорядочиванием пакетов.
- [x] Учитывать потери, дубликаты и слишком старые sequence numbers.
- [x] Смешивать готовые PCM-потоки перед передачей в один audio player.
- [x] Удалять decoder и jitter buffer после ухода пользователя или timeout.

Целевая схема:

```text
UDP packet
    -> stream[SenderID]
    -> sequence/jitter buffer
    -> Opus decoder
    -> PCM mixer
    -> player
```

Критерий готовности: речь двух и более одновременных отправителей не смешивает
состояние Opus-декодеров и корректно воспроизводится при перестановке и потере
UDP-пакетов.

### Этап 4. Сделать lifecycle предсказуемым

- [x] Передавать `context.Context` в серверный receive loop.
- [x] Завершать сервер при `SIGINT`/`SIGTERM` и корректно обрабатывать
  `net.ErrClosed`.
- [x] Сделать `MalgoRecorder.Close` идемпотентным и освобождать все ресурсы,
  даже если `device.Stop` вернул ошибку.
- [x] Гарантированно разблокировать `RecordLoop` и `PlaybackLoop` при отмене
  контекста.
- [x] Разделить клиентскую точку входа на `main()` и `run() error`; оставить
  `log.Fatal` только в `main`, чтобы освобождение ресурсов через `defer`
  выполнялось до завершения процесса.
- [x] Заменить ручной подсчёт goroutine на единый supervisor/errgroup-подобный
  механизм: первая ошибка отменяет остальные loops, затем выполняется ожидание.
- [x] Добавить тесты на shutdown клиента и сервера без зависаний и утечек
  goroutine.

Критерий готовности: клиент и сервер завершаются за ограниченное время после
сигнала или ошибки любого компонента.

### Этап 5. Закрыть небезопасный доступ к состоянию сервера

- [x] Не возвращать из `Hub` изменяемые `*Session` после освобождения mutex.
- [x] Возвращать snapshot session либо выполнять операции над session внутри
  методов `Hub`.
- [x] Хранить изменение адреса, канала и `LastSeen` только за одной границей
  синхронизации.
- [x] Добавить конкурентные тесты `JoinChannel`, `Touch`, routing и cleanup.
- [x] Настроить рабочий Windows toolchain и включить `go test -race ./...` в CI.

Критерий готовности: race detector проходит при параллельной маршрутизации,
смене канала и очистке неактивных сессий.

### Этап 6. Декомпозиция кода

Клиентский pipeline разделён внутри существующего пакета по ответственности:

```text
internal/client/
    state.go       конкурентное состояние client session и pending requests
    request.go     handshake, request/retry и timeout
    control.go     join, heartbeat и обработка control responses
    receive.go     разделение входящих UDP media/control packets
    capture.go     recorder, encoder и отправка voice packets
    playback.go    запись готового PCM в audio player
    command.go     команды из stdin
    jitter.go      per-user переупорядочивание media frames
    decode.go      per-user Opus decoders
    mixer.go       смешивание PCM-потоков
```

Серверный UDP router также разделён внутри `package voice` по ответственности:

```text
internal/voice/
    server.go      чтение UDP datagram и диспетчеризация по типу пакета
    control.go     hello, join, heartbeat, disconnect и ответы с ошибками
    media.go       проверка и маршрутизация voice-пакетов получателям
    delivery.go    отправка пакетов и проверка адреса сессии
    hub.go         потокобезопасный реестр сессий и каналов
    session.go     состояние отдельной сессии
    cache.go       кэш ответов на повторные control requests
```

После стабилизации этих границ следующим архитектурным шагом можно выделить
самостоятельные пакеты:

```text
internal/session/          Session и Hub
internal/server/control/   hello, join, heartbeat, request cache
internal/server/media/     проверка и маршрутизация voice-пакетов
internal/media/            stream, jitter buffer, decoder, mixer
internal/protocol/         wire format и валидация
internal/transport/udp/    только UDP I/O
```

Перенос следует делать после исправления поведения и покрытия тестами, чтобы не
совмещать функциональные изменения с массовым перемещением кода.

### Этап 7. Безопасность и эксплуатация

- [x] Заменить последовательные session ID на криптографически случайные
  идентификаторы или session token.
- [x] Отделить UDP I/O от кодирования датаграмм через внедряемый codec и
  сохранить текущий wire format в `PlainDatagramCodec`.
- [x] Укрепить контракт codec/transport: передавать recipient context,
  соблюдать общий wire limit, безопасно отбрасывать rejected datagrams и
  разделить connected/unconnected API типами.
- Отложенные security, rate limiting, metrics и альтернативные transport-задачи
  ведутся отдельно в [`readme_docs/backlog.md`](readme_docs/backlog.md).

### Следующий активный этап. Модель каналов и синхронизация состояния

- [x] Ввести самостоятельную server-side модель `Channel` со стабильным ID и
  иерархией для будущего UI.
- [ ] Добавить snapshot каналов, пользователей и их текущего размещения.
- [ ] Хранить полученные snapshots в клиентском `State`.
- [ ] Добавить server events `UserJoined`, `UserLeft` и `UserMoved` после
  стабилизации начальной синхронизации.

Сначала следует реализовать snapshots через существующий надёжный
request/response-контур. События добавляются вторым шагом, чтобы не смешивать
формат данных, начальную синхронизацию и live updates в одном изменении.
Требования, полученные из целевого интерфейса каналов, описаны в
[`readme_docs/channel-ui.md`](readme_docs/channel-ui.md).

## Дальнейшая разработка

```text
Channel domain model
    -> paged state snapshot
    -> revisioned server events
    -> UI-facing client service
    -> voice controls
    -> first GUI
```

Доменная модель и переход server routing на `ChannelID` выполнены в Patch 2.
Следующее небольшое изменение — конфигурация стартового дерева каналов из
[`readme_docs/patch-3.md`](readme_docs/patch-3.md). После неё отдельным Patch 4
начнётся paged state snapshot через существующий request/response-контур.
Полный порядок и критерии готовности описаны в
[`readme_docs/development-plan.md`](readme_docs/development-plan.md).
