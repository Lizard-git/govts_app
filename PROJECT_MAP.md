# Карта проекта Go Voice

Документ описывает фактическое состояние рабочей копии проекта: назначение каталогов и файлов, точки входа, потоки данных, зависимости и тестовое покрытие. Это карта текущей реализации, а не план целевой архитектуры. Продуктовые планы вынесены в `go-voice-roadmap.md`, а краткая инструкция по запуску — в `README.md`.

## 1. Назначение проекта

`govots` — полноценное развиваемое приложение для голосового общения и совместной работы в реальном времени. Проект создаёт самостоятельную современную платформу, которая должна превзойти традиционные голосовые клиенты качеством связи, удобством и набором новых возможностей. Текущая UDP-реализация является фундаментом продукта: несколько клиентов уже могут проходить handshake, входить в зарегистрированные сервером каналы и обмениваться Opus-аудио.

Цели проекта:

- обеспечить качественную голосовую связь с низкой задержкой и устойчивостью к реальным сетевым условиям;
- поддерживать каналы, одновременную речь нескольких участников и гибкое управление пользовательскими аудиопотоками;
- развить надёжную синхронизацию состояния, reconnect и восстановление сессий;
- добавить современные средства управления голосом: PTT, VAD, mute/deafen, индивидуальную громкость и обработку звука;
- обеспечить аутентификацию, шифрование, защиту от подмены и безопасное управление доступом;
- предоставить удобный GUI и расширяемую основу для новых сценариев общения и совместной работы;
- превратить текущую архитектуру в production-ready систему с наблюдаемостью, масштабированием и дальнейшими возможностями, выходящими за рамки традиционных голосовых клиентов.

Сервер в текущей версии звук не декодирует: он проверяет сессию отправителя, находит участников того же канала и пересылает исходный payload всем получателям, кроме отправителя. Такая модель сохраняет серверный media path лёгким и оставляет пространство для дальнейшего развития транспортной и медиаархитектуры.

Основные параметры текущей реализации:

| Параметр | Значение |
|---|---:|
| UDP-адрес сервера | `0.0.0.0:9000` |
| Адрес, к которому подключается клиент | `127.0.0.1:9000` |
| Частота дискретизации | 48 000 Гц |
| Каналы аудио | 1, mono |
| Размер аудиокадра | 960 samples, то есть 20 мс |
| Кодек | Opus |
| Заголовок протокола | 17 байт |
| Максимальный payload | 1200 байт |
| Максимальная UDP-датаграмма | 1217 байт |
| Heartbeat | каждые 5 секунд |
| Тайм-аут сессии | 30 секунд |
| Очистка сессий и кэша | каждые 5 секунд |
| Попытки control-запроса | 3 |
| Тайм-аут одной попытки | 3 секунды |
| Глубина jitter buffer | 3 кадра |

## 2. Архитектура верхнего уровня

```text
┌──────────────────────────── client A ────────────────────────────┐
│ microphone → PCM → Opus → VoicePacket → UDP                     │
│                                                                  │
│ UDP → demux → per-sender jitter → per-sender Opus decoder       │
│     → PCM mixer → Oto player                                     │
└──────────────────────────────┬───────────────────────────────────┘
                               │ UDP :9000
                               ▼
┌──────────────────────────── server ──────────────────────────────┐
│ decode packet → validate session/address → touch LastSeen        │
│               → find same-channel recipients → forward bytes    │
│                                                                  │
│ Hub: sessions/channels     RequestCache: idempotent control      │
└──────────────────────────────┬───────────────────────────────────┘
                               │ UDP
                               ▼
                       client B, client C, ...
```

В одном UDP-сокете мультиплексированы две логические плоскости:

- media plane — `PacketVoice`, где payload содержит Opus-кадр, а `Sequence` задаёт порядок кадров;
- control plane — handshake, heartbeat, disconnect, join и ответы, где `RequestID` связывает запрос с ответом и позволяет безопасно повторять запрос.

Слои зависят друг от друга сверху вниз:

```text
cmd/client2 ─┬→ internal/client ─┬→ internal/audio
             │                   ├→ internal/protocol
             │                   └→ internal/transport/udp
             └→ internal/audio

cmd/server ──┬→ internal/server → internal/voice
             ├→ internal/voice ─┬→ internal/protocol
             │                  └→ internal/transport/udp
             └→ internal/transport/udp

internal/transport/udp → internal/protocol.DatagramCodec
```

## 3. Главные сценарии выполнения

### 3.1. Запуск клиента и handshake

1. `cmd/client2/main.go` читает `-name` и `-channel`, создаёт контекст, реагирующий на `SIGINT`/`SIGTERM`, открывает connected UDP socket и передаёт владение им `udp.ClientPacketConn`.
2. `internal/client/request.go` отправляет `PacketHello` с криптографически случайным ненулевым `RequestID` и именем в payload.
3. `internal/voice/control.go` проверяет имя и ищет `(IP:port, RequestID)` в handshake-кэше.
4. При cache miss `Hub.CreateSession` выдаёт криптографически случайный ненулевой `SessionID`; сервер сохраняет и отправляет `PacketHelloAck`.
5. После `HelloAck` клиент привязывает `ClientPacketConn` к выданному `SessionID`; при потере ACK он повторяет тот же Hello с тем же request ID, а сервер возвращает закэшированный ответ, не создавая вторую сессию.

### 3.2. Присоединение к каналу

1. Клиент сначала запускает receive/control и входящий аудиоконвейер, потому что ответ на join приходит через `ReceiveLoop` и `ControlLoop`.
2. `JoinChannel` вызывает общий `DoRequest`: регистрирует ожидающий ответ в `State.pending`, присваивает новый `RequestID` и до трёх раз отправляет один логический запрос.
3. Сервер валидирует соответствие `SessionID` исходному `IP:port`, проверяет request cache и меняет канал в `Hub` только при cache miss.
4. `PacketJoinChannelAck` проходит через `ReceiveLoop → controlCh → ControlLoop → State.CompleteRequest`.
5. Клиент сверяет подтверждённый `ChannelID`, повторно загружает полный snapshot и только после успеха запускает захват микрофона. Поэтому voice-пакеты не отправляются до подтверждённого join и финальной синхронизации.

### 3.3. Исходящий звук

```text
MalgoRecorder
  → RecordLoop: []int16 / PCMFrame
  → EncodeLoop: Opus / Frame
  → SendLoop: SessionID + возрастающий Sequence / VoicePacket
  → ClientPacketConn.SendPacket
  → DatagramCodec.Encode(client→server, session, endpoint)
```

Кадр содержит 960 mono samples при 48 кГц, то есть примерно 20 мс аудио. Нумерация `Sequence` начинается с 1 для каждой запущенной клиентской сессии и естественно допускает переполнение `uint32`.

### 3.4. Маршрутизация на сервере

```text
ServerPacketConn.ReadPacket
  → DatagramCodec.Decode(client→server, endpoint)
  → voice.ServeUDP
  → voice.HandlePacket
  → HandleVoicePacket
  → ValidateSessionAddr + Hub.Touch
  → Hub.RecipientsFor
  → SendToSessions
```

Сервер не преобразует Opus payload. Отправитель без канала отклоняется; получателями становятся снимки всех остальных сессий в том же канале.

### 3.5. Входящий звук

```text
ClientPacketConn.ReceivePacket
  → DatagramCodec.Decode(server→client, session, endpoint)
  → ReceiveLoop
  → MediaFrame{SenderID, Sequence}
  → JitterLoop: отдельный буфер на SenderID
  → DecodeLoop: отдельный Opus decoder на SenderID
  → MixLoop: один очередной PCM-кадр от каждого активного отправителя
  → PlaybackLoop
  → OtoPlayer
```

Раздельные jitter buffers и декодеры принципиальны: Opus-декодер хранит состояние потока, поэтому один экземпляр нельзя безопасно использовать попеременно для разных говорящих.

### 3.6. Остановка и очистка

- Любая неожиданная ошибка клиентского loop вызывает `loopSupervisor.Cancel`, после чего останавливаются остальные loops.
- До ожидания goroutine закрываются recorder и session-scoped Oto player, чтобы разблокировать потенциально блокирующие `Read`/`Write`. Единственный `OtoOutput`/context живёт до завершения процесса и переиспользуется после reconnect.
- Клиент в конце пытается отправить `PacketDisconnect`, затем `run` закрывает UDP socket.
- Сервер удаляет сессию сразу по disconnect либо через 30 секунд неактивности. Связанные записи request cache также удаляются.
- Отмена серверного контекста устанавливает немедленный read deadline и выводит `ServeUDP` из блокирующего чтения.

## 4. Бинарный UDP-протокол

Каждая датаграмма имеет одинаковый big-endian заголовок:

| Смещение | Поле | Тип | Назначение |
|---:|---|---|---|
| `0` | `Type` | `uint8` | Тип пакета, значение 1–8 |
| `1..8` | `SessionID` | `uint64` | Сессия клиента/отправителя |
| `9..12` | `Sequence` | `uint32` | Порядок voice-кадров |
| `13..16` | `RequestID` | `uint32` | Корреляция control request/response |
| `17..N` | `Payload` | `[]byte` | Имя, канал, ошибка или Opus-данные |

| Type | Константа | Направление и смысл |
|---:|---|---|
| 1 | `PacketHello` | клиент → сервер; имя клиента в payload |
| 2 | `PacketVoice` | клиент → сервер → клиенты; Opus в payload |
| 3 | `PacketHelloAck` | сервер → клиент; назначенный `SessionID` |
| 4 | `PacketHeartbeat` | клиент → сервер; обновление `LastSeen` |
| 5 | `PacketDisconnect` | клиент → сервер; явное удаление сессии |
| 6 | `PacketJoinChannel` | клиент → сервер; big-endian `ChannelID` |
| 7 | `PacketJoinChannelAck` | сервер → клиент; `ChannelID` и `StateRevision` |
| 8 | `PacketError` | сервер → клиент; текст ошибки в payload |
| 9 | `PacketStateSnapshotRequest` | клиент → сервер; metadata/page request |
| 10 | `PacketStateSnapshotAck` | сервер → клиент; versioned snapshot response |

## 5. Карта каталогов и файлов

### Корень проекта

#### `go.mod`

Манифест Go-модуля `example.com/go-voice-mvp`, рассчитанный на Go `1.27.1`. Прямые зависимости:

- `github.com/gen2brain/malgo` — захват PCM с системного устройства;
- `github.com/ebitengine/oto/v3` — воспроизведение PCM;
- `github.com/pion/opus` — Opus encoder/decoder.

Также фиксирует косвенные `purego` и `golang.org/x/sys`; последний нужен, в частности, для распознавания Windows-ошибки `WSAEMSGSIZE`.

#### `go.sum`

Контрольные суммы модулей из `go.mod` и их `go.mod`-файлов. Обеспечивает воспроизводимую и проверяемую загрузку зависимостей; вручную обычно не редактируется.

#### `README.md`

Основная пользовательская документация: позиционирование проекта, фактическая архитектура, команды запуска, формат UDP-пакета, реализованные возможности, ограничения и поэтапный план технического развития. Это лучший первый файл для запуска проекта, но подробности каждого компонента находятся в текущей карте.

#### `go-voice-roadmap.md`

Расширенная продуктовая и техническая дорожная карта. Описывает этапы reliable control protocol, синхронизации server/client state, multi-speaker audio, jitter/PLC, mixer, voice controls, reconnect, security, разделение control/media transport, GUI и production hardening. Часть статусов в этом документе может отставать от кода: фактическое состояние следует проверять по исходникам и тестам.

#### `PROJECT_MAP.md`

Этот документ. Даёт архитектурный обзор и подробный индекс рабочей копии по файлам; его следует обновлять при добавлении пакетов, изменении wire format или перестройке runtime pipeline.

#### `readme_docs/patch-1.md`

Завершённый технический патч укрепления границы `DatagramCodec`/UDP transport. Фиксирует исходные риски, целевые контракты context, размера, ошибок и владения socket, выполненные шаги и тестовую матрицу. Не реализованные security-идеи из него перенесены в backlog без назначения следующим патчем.

#### `readme_docs/patch-2.md`

Завершённый чеклист доменной модели каналов: общие channel domain types, server-side registry и revision, миграция server routing с имени канала на `ChannelID`, сохранение текущего wire format и тесты контрактов.

#### `readme_docs/patch-3.md`

Завершённый план конфигурации стартового дерева каналов: нейтральный
`BootstrapSource`, строгий bounded JSON как его первая внешняя реализация,
атомарный bootstrap Hub, встроенный канал `main`, server CLI flag, fail-fast
validation, локальную read-only консоль состояния и тесты без изменения wire
format. SQLite сможет заменить источник чтения для bootstrap, но стабильные ID
и writable repository остаются отдельным этапом persistence. Удалённый RCON
отложен до security/permissions, а paged state snapshot выделен в следующий
патч.

#### `readme_docs/patch-4.md`

Исполняемый план initial state synchronization: versioned binary metadata,
paged channels/participants, проверка одной `StateRevision`, атомарная
публикация client snapshot и полный перевод join payload на `ChannelID` без
name-based compatibility layer. Live events и GUI в патч не входят.

#### `readme_docs/backlog.md`

Единый список направлений, которые сознательно не входят в ближайшую разработку: security и шифрование, защита от нагрузки, наблюдаемость, альтернативные transports и поздние продуктовые расширения. Активные этапы из этого файла исключены.

#### `readme_docs/channel-ui.md`

Требования к backend-модели, полученные из визуального ориентира будущего интерфейса каналов: иерархия, метаданные, участники, производные counters, аудиопрофиль и границы ещё не реализованных UI-действий. Определяет влияние UI на следующий state-synchronization patch без преждевременного выбора GUI framework.

#### `readme_docs/development-plan.md`

Единственный источник порядка активной разработки. Ведёт проект от authoritative channel domain model через paged snapshots, revisioned events и UI-facing client API к voice controls и первой версии GUI; для каждого этапа фиксирует границы и критерии готовности.

#### `readme_docs/patch-5.md`

Выполненный план lifecycle: подтверждаемый heartbeat, автоматический
reconnect с задержками `1s → 2s → 4s → 4s...`, безопасная смена session binding
и временное консольное дерево каналов/участников поверх `ServerSnapshot`.

#### `package.json`

Не содержит JavaScript-кода или npm-зависимостей; используется только как набор удобных npm run scripts для Go-команд. `dev:server` запускает сервер. `dev:client-42` и `dev:client-43` сейчас запускают одинаковую команду с одним именем `G712`, поэтому для моделирования разных участников их параметры стоит различать вручную.

#### `.gitignore`

Исключает настройки IDE, Go-бинарники и результаты тестов, каталоги сборки/coverage, архивы, временные файлы и логи. Благодаря правилам `*.exe` и `*.zip` локальные `gomon.exe` и `go.zip` не должны становиться частью репозитория.

#### `govots.iml`

Локальное описание модуля IntelliJ IDEA/GoLand. Включает поддержку Go module и исключает каталог `readme` из content root. Это IDE-метаданные, на сборку через Go toolchain не влияют и по `.gitignore` не предназначены для хранения в Git.

#### `go.zip`

Локальный архив более раннего снимка проекта. Внутри присутствуют уже отсутствующие в рабочей структуре `cmd/client`, `cmd/main.go` и монолитные `internal/client/pipeline.go`, `internal/voice/router.go`. Это не runtime-зависимость и не источник актуальной архитектуры.

#### `gomon.exe`

Локальный Windows-бинарник. Исходный Go-код его не импортирует и команды запуска на него не ссылаются; для сборки, тестов и работы текущих `cmd/server`/`cmd/client2` он не требуется.

### `configs/` — примеры конфигурации сервера

#### `configs/server.example.json`

Строгий пример стартового дерева: корневой `main` и дочерние `gaming`/`music`
с position, topic и `max_users`. Runtime `ChannelID`, channel type и
аудиопрофиль намеренно не задаются в JSON. Файл можно передать серверу через
`-config configs/server.example.json`.

### `.github/`

#### `.github/workflows/test.yml`

CI workflow `Go checks`, запускаемый на push, pull request и вручную. На Ubuntu checkout-ит репозиторий, выбирает версию Go из `go.mod`, устанавливает ALSA headers (`libasound2-dev`), выполняет `go vet ./...` и `go test -race ./...`. Разрешения workflow ограничены чтением содержимого.

### `cmd/server/` — исполняемый сервер

#### `cmd/server/main.go`

Точка входа UDP-сервера.

- `main` разбирает `-config` и остаётся единственным местом с `log.Fatal`.
- `run` создаёт signal-aware context и до открытия UDP socket атомарно строит
  Hub через выбранный `BootstrapSource`.
- Без файла используется встроенный канал `main`; с файлом загружается строгое
  пользовательское дерево поверх системного `default`.
- Параллельно запускает `server.CleanupLoop` и локальную read-only консоль, а в
  основном потоке — `voice.ServeUDP`.
- После завершения одного контура отменяет контекст, ждёт cleanup goroutine и объединяет независимые ошибки через `errors.Join`.

Файл отвечает только за composition root и lifecycle процесса; протокол и бизнес-логика вынесены в `internal`.

### `cmd/client2/` — исполняемый голосовой клиент

#### `cmd/client2/main.go`

Минимальная точка входа клиента.

- Объявляет flags `-name` (обязательный) и `-channel` (по умолчанию `default`).
- Подключается к жёстко заданному `127.0.0.1:9000`.
- Выполняет handshake до создания аудиоресурсов.
- Передаёт полученный `SessionID` в `runSession`.
- Гарантированно закрывает UDP connection и сохраняет ошибку закрытия через `errors.Join`.

#### `cmd/client2/runtime.go`

Composition root одной активной клиентской сессии и главный файл связывания pipeline.

- Задаёт аудиопараметры `48000/mono/960`.
- Создаёт каналы между loops: исходные PCM/Opus, входящие media/control, упорядоченные и декодированные кадры, mixed PCM.
- Получает app-lifetime `OtoOutput`, создаёт из него отдельный Oto player для
  текущей сетевой сессии и создаёт Opus encoder.
- До join запускает receive, control, heartbeat, jitter, decode, mix и playback.
- После подтверждённого join создаёт Malgo recorder и запускает record, encode и send.
- Запускает CLI-команды из stdin.
- При любой ошибке закрывает блокирующие аудиоресурсы, ждёт loops и отправляет disconnect.
- `wrapError` добавляет контекст операции, не создавая ошибку для `nil`.

#### `cmd/client2/supervisor.go`

Локальный errgroup-подобный координатор goroutine.

- Хранит общий дочерний context и `WaitGroup`.
- `Go` запускает loop; завершение любого loop сигнализирует `done` и отменяет остальные.
- `Wait` ждёт первого сигнала, затем вызывает shutdown callback до `WaitGroup.Wait`, что важно для разблокировки audio I/O.
- `Shutdown` инициирует тот же порядок явно, например при ошибке join.
- Собирает все неожиданные ошибки потокобезопасно и объединяет их.
- `unexpectedLoopError` считает штатными `context.Canceled`, `io.EOF` и `io.ErrClosedPipe`.

#### `cmd/client2/supervisor_test.go`

Проверяет lifecycle supervisor: ошибка одного loop отменяет другой; shutdown callback вызывается до ожидания заблокированной goroutine; явный `Shutdown` завершает supervisor даже если ни один loop сам не успел вернуть результат.

### `internal/audio/` — типы, кодек и системные устройства

#### `internal/audio/audio.go`

Общие контракты аудиослоя.

- `Frame` — закодированный исходящий кадр.
- `MediaFrame` — входящий закодированный кадр с сохранёнными `SenderID` и `Sequence`.
- `PCMFrame` — PCM для локальной записи или итогового воспроизведения.
- `MediaPCMFrame` — декодированный PCM конкретного удалённого отправителя.
- `Encoder`, `Decoder`, `Recorder`, `Player` — малые интерфейсы, отделяющие pipeline от реализаций.
- `CodecConfig` — общие sample rate, channel count и frame size.
- `PCM16Encoder` и `int16ToBytes` — простой little-endian PCM16 encoder и общая функция сериализации samples; Opus encoder также использует эту функцию.

#### `internal/audio/malgo.go`

Реализация `Recorder` поверх `gen2brain/malgo`/miniaudio.

- Инициализирует capture device в signed 16-bit PCM с параметрами `CodecConfig`.
- Real-time callback копирует входной chunk в буферизованный `dataCh`; при переполнении канал не блокирует аудиопоток и отбрасывает chunk.
- `Read` собирает требуемое число `int16` из произвольных byte chunks, сохраняя неиспользованный хвост в `pending`.
- `Close` идемпотентен через `sync.Once`: останавливает и uninit-ит device, закрывает канал, освобождает context, объединяет ошибки.

#### `internal/audio/opus.go`

Адаптеры `pion/opus` к интерфейсам проекта.

- `OpusEncoder` конвертирует `[]int16` в little-endian bytes и кодирует их во временный буфер максимум 4000 байт.
- `OpusDecoder` выделяет PCM-буфер по `SamplesPerFrame × Channels` и возвращает только реально декодированное число samples.
- Encoder создаётся один на локальный микрофон, decoder — отдельный на каждого удалённого `SenderID`.

#### `internal/audio/player.go`

Реализация app-lifetime audio output и session-scoped `Player` через Oto.

- `OtoOutput` единственный раз за процесс создаёт Oto context в формате signed
  PCM16 little-endian и ждёт готовности не более 5 секунд; повторно создавать
  context при reconnect нельзя по контракту Oto.
- `OtoOutput.NewPlayer` для каждой сетевой сессии создаёт отдельные `io.Pipe` и
  Oto player внутри общего context.
- Размер внутреннего буфера равен одному PCM-кадру.
- `Write` сериализует samples и пишет в pipe.
- `Close` идемпотентно закрывает reader раньше writer, ставит session player на
  паузу и очищает его buffer, немедленно разблокируя конкурентный `Write` во
  время shutdown и не уничтожая общий Oto context.

### `internal/protocol/` — wire format

#### `internal/protocol/packet.go`

Единственный источник истины для бинарного формата UDP-пакета.

- Объявляет конкретные ошибки формата и верхнеуровневую категорию `ErrRejectedDatagram` для безопасно отбрасываемого входа.
- Задаёт десять packet types и границу `PacketEnd` для валидации.
- `VoicePacket` — универсальный envelope control и voice сообщений.
- `HeaderSize`, `MaxPayloadSize`, `MaxWireDatagramSize` фиксируют сетевые лимиты; `MaxDatagramSize` временно оставлен совместимым alias.
- `EncodePacket` проверяет type и payload, затем пишет big-endian header.
- `DecodePacket` проверяет полный размер и type до разбора полей и сохраняет через `errors.Is` одновременно категорию rejected input и конкретную причину.
- `NewVoicePacket` и `NewErrorPacket` — конструкторы двух частых вариантов.
- `packetName`, `makePayload`, `encodeSessionID` — внутренние вспомогательные функции; основной encode path использует `encodeHeader`.

#### `internal/protocol/join.go`

Строгий ID-based payload-контракт join: request содержит ровно один big-endian
`ChannelID`, ACK — `ChannelID` и `StateRevision`. Нулевые значения, усечённый и
расширенный payload отклоняются.

#### `internal/protocol/snapshot.go`

Versioned бинарный контракт metadata/channels/participants. Запрос содержит
kind, ожидаемую revision, offset и limit; ответ — статус `OK` или
`RevisionChanged`, progression страницы и типизированные items. Codec проверяет
каноническую структуру, UTF-8/лимиты строк, enum-поля и предел payload 1200 байт.

#### `internal/protocol/codec.go`

Алгоритм-независимая граница между логическим `VoicePacket` и сетевой датаграммой.

- `DatagramCodec` определяет `Encode` и `Decode`, принимающие `DatagramContext` с направлением, владельцем ключа и удалённым endpoint.
- `PlainDatagramCodec` сохраняет текущий незашифрованный 17-байтный формат и делегирует существующим `EncodePacket`/`DecodePacket`.
- Общий wire limit принадлежит протоколу, а не codec; будущий защищённый codec должен разместить envelope и authentication tag внутри него.
- При серверной отправке `KeyOwnerID` обозначает получателя независимо от `VoicePacket.SessionID`; при серверном чтении ID пока равен нулю и будущий codec должен извлечь предварительный идентификатор из открытой части versioned envelope, используя endpoint как дополнительный контекст.

#### `internal/protocol/proto_test.go`

Покрывает допустимые и недопустимые type values, round trip всех полей и payload, короткий пакет, HelloAck, максимальный payload, превышение лимитов при encode/decode и отклонение неизвестного типа. Эти тесты защищают совместимость wire format.

### `internal/transport/udp/` — UDP I/O без бизнес-логики

#### `internal/transport/udp/client.go`

Создание raw connected `net.UDPConn`.

- `ConnectUDP` принимает только строку с IP, а не DNS hostname, и выполняет `net.DialUDP`.
- Публичных plaintext read/write helpers здесь больше нет: дальнейшая работа выполняется только через `ClientPacketConn`.

#### `internal/transport/udp/client_test.go`

Отправляет клиенту oversized UDP datagram и проверяет, что `ClientPacketConn.ReceivePacket` возвращает одновременно `protocol.ErrRejectedDatagram` и `protocol.ErrPacketTooLarge`, а не передаёт усечённые данные выше по pipeline.

#### `internal/transport/udp/server.go`

Создание raw unconnected UDP socket.

- `ListenUDP` слушает все IPv4-интерфейсы на переданном порту.
- Публичных plaintext read/write helpers здесь больше нет: socket передаётся `ServerPacketConn`.

#### `internal/transport/udp/server_test.go`

Проверяет серверную половину защиты от слишком большой датаграммы и обе категории ошибки: `protocol.ErrRejectedDatagram` и `protocol.ErrPacketTooLarge`.

#### `internal/transport/udp/packet_conn.go`

Два типобезопасных пакетных фасада над общей внутренней реализацией, связывающей `net.UDPConn` с внедрённым `protocol.DatagramCodec`.

- `ClientPacketConn` предоставляет только `SendPacket`/`ReceivePacket` для connected socket; `BindSession` один раз привязывает будущий security context к локальному `SessionID`.
- `ServerPacketConn` предоставляет только `ReadPacket`/`WritePacket` для unconnected socket; `WritePacket` принимает recipient ID отдельно от логического пакета.
- Конструкторы проверяют режим socket. При успехе владение raw connection переходит фасаду, при ошибке остаётся у вызывающего; `Close` идемпотентен и разблокирует чтение.
- Read buffer всегда равен `MaxWireDatagramSize + 1`, а превышение единого предела отклоняется на входе и выходе независимо от codec.
- Отдельные mutex сериализуют чтение и полный encode/write path; общий codec mutex позволяет позднее использовать stateful codec с nonce/replay state безопасно при одновременных heartbeat, voice и control sends.
- Методы lifecycle/deadline/address скрывают raw socket от верхних слоёв, но сохраняют необходимые операции завершения.

#### `internal/transport/udp/packet_conn_test.go`

Проверяет передачу полного context в codec, различие sender/recipient ID, общий wire limit, проверку socket mode, правила `BindSession`, идемпотентный `Close` и разблокирование read.

#### `internal/transport/udp/msgsize_windows.go`

Windows-only реализация, выбранная build tag `windows`. Распознаёт `windows.WSAEMSGSIZE`, которое `net.UDPConn` может вернуть при усечении входящей датаграммы, и позволяет преобразовать его в доменную ошибку `ErrPacketTooLarge`.

#### `internal/transport/udp/msgsize_other.go`

Реализация для всех не-Windows платформ (`!windows`). Возвращает `false`: там oversized packet обнаруживается по длине дополнительного байта, а специальная системная ошибка Windows отсутствует.

### `internal/client/` — состояние и конкурентный аудиоконвейер клиента

#### `internal/client/state.go`

Потокобезопасное состояние активной клиентской сессии.

- Под mutex хранит `sessionID`, имя, текущий `ChannelID`, глубокую копию
  последнего полного `ServerSnapshot` и map ожидающих control-запросов.
- `atomic.Uint32` выдаёт монотонные request IDs для запросов после handshake.
- `RegisterRequest` создаёт одноэлементный response channel.
- `CompleteRequest` атомарно извлекает pending request, затем вне lock доставляет ответ и закрывает канал.
- `CancelRequest` очищает запись при timeout/cancel.
- `Snapshot` возвращает глубокую копию, а `ReplaceSnapshot` атомарно публикует
  только полностью собранное состояние.

#### `internal/client/request.go`

Надёжные request/response операции поверх UDP.

- `PerformHandshake` валидирует имя, генерирует случайный request ID и запускает retry-цикл до создания обычного `State`/`ReceiveLoop`.
- Handshake сам временно управляет read deadline и синхронно читает ACK из socket.
- `DoRequest` работает уже внутри runtime: регистрирует pending response в `State`, повторяет отправку до трёх раз и ждёт `ControlLoop` через канал.
- Один `RequestID` сохраняется на всех попытках, что вместе с серверным cache даёт идемпотентность.
- `PacketError`, timeout и context cancellation превращаются в осмысленные ошибки.

#### `internal/client/snapshot.go`

Загружает metadata, затем все страницы каналов и участников одной revision.
Проверяет totals до allocation, progression offset, сортировку, уникальность ID,
иерархию и ссылки участников. При `RevisionChanged` полностью повторяет загрузку
не более трёх раз. Здесь же имя или числовой selector локально разрешается в
однозначный `ChannelID`.

#### `internal/client/control.go`

Control plane после handshake.

- `ControlLoop` принимает демультиплексированные control-пакеты, игнорирует чужой `SessionID` и завершает pending join/snapshot/error requests.
- `HeartbeatLoop` каждые пять секунд отправляет session heartbeat.
- `Disconnect` посылает одноразовый пакет без ожидания ACK.
- `JoinChannel` использует `DoRequest`, отправляет только `ChannelID`, проверяет
  ID и revision строгого ACK, затем обновляет `State`.

#### `internal/client/command.go`

Простой интерактивный CLI поверх stdin. `/join <id|name>` разрешает канал по
локальному snapshot, выполняет ID-join и после ACK обновляет snapshot; `/quit`
отменяет общий context. Завершение stdin само по себе не отменяет клиент.

#### `internal/client/capture.go`

Три последовательных loop исходящего media pipeline.

- `RecordLoop` читает один PCM frame из `Recorder`, присваивает длительность 20 мс и закрывает `pcmCh` при завершении.
- `EncodeLoop` кодирует PCM через внедрённый `audio.Encoder`, сохраняет duration и закрывает `audioCh`.
- `SendLoop` оборачивает encoded frame в `PacketVoice`, назначает возрастающий `Sequence` и отправляет через UDP.
- Все передачи между стадиями учитывают отмену context и создают естественный backpressure через небуферизованные runtime channels.

#### `internal/client/receive.go`

Единственная goroutine, читающая UDP socket после handshake.

- Ставит read deadline на 500 мс, чтобы регулярно проверять отмену context.
- Преобразует `PacketVoice` в `MediaFrame`, сохраняя `SessionID` как `SenderID` и сетевой `Sequence`.
- Все остальные типы отправляет в `controlCh`.
- При выходе закрывает оба выходных канала, каскадно завершая downstream loops.

#### `internal/client/jitter.go`

Переупорядочивание входящих кадров отдельно для каждого отправителя.

- `jitterBuffer` хранит ожидаемый sequence, pending map, состояние запуска и статистику `Lost/Duplicates/TooOld`.
- До старта накапливает `depth` кадров; затем выдаёт непрерывную последовательность.
- `Tick` после нескольких интервалов ожидания считает пропуск потерянным и продвигается к ближайшему кадру.
- `sequenceBefore` корректно сравнивает номера при wraparound `uint32`.
- `streamJitterBuffers` маршрутизирует по `SenderID` и удаляет неактивный stream через 30 секунд.
- `JitterLoop` тикает каждые 20 мс, flush-ит остатки при закрытии входа и закрывает `orderedCh`.

Ограничение: реализован фиксированный буфер без адаптивной задержки и Opus packet-loss concealment.

#### `internal/client/jitter_test.go`

Проверяет reorder, переход через пропущенный кадр при заполненном окне, выпуск короткого потока по timer tick, отбрасывание duplicate/too-old кадров, wraparound sequence, независимость отправителей и удаление неактивных stream buffers.

#### `internal/client/decode.go`

Декодирование упорядоченных Opus-потоков.

- `DecoderFactory` позволяет создавать новый stateful decoder для каждого sender и подменять его в тестах.
- `streamDecoders` хранит decoder и `lastSeen` по `SenderID`.
- Результат сохраняет sender/sequence metadata до стадии mixer.
- Неактивные decoder instances удаляются через 30 секунд; cleanup запускается раз в 5 секунд.
- Любая ошибка создания или decode завершает `DecodeLoop` и через supervisor инициирует общий shutdown.

#### `internal/client/decode_test.go`

С помощью fake decoder доказывает, что разные отправители получают независимое состояние декодера, а неактивные decoder entries удаляются по timeout.

#### `internal/client/mixer.go`

Объединяет PCM нескольких говорящих в один playback stream.

- Для каждого sender хранится FIFO decoded frames.
- На каждом `Mix` берётся не более одного кадра от каждого sender.
- Samples суммируются по индексам; разная длина поддерживается выбором максимальной длины.
- Сумма насыщается границами `int16`, предотвращая integer wrap/clipping overflow.
- `MixLoop` выдаёт кадр по ticker, по умолчанию раз в 20 мс; при закрытии входа дренирует остаток очередей.

#### `internal/client/mixer_test.go`

Проверяет сложение samples с насыщением по минимуму/максимуму `int16` и правило «один кадр от каждого sender за один mix tick».

#### `internal/client/playback.go`

Последняя стадия входящего pipeline. Последовательно передаёт mixed `PCMFrame.Samples` внедрённому `audio.Player`, завершается по context, закрытию канала или ошибке устройства. Закрытием самого player управляет runtime, а не loop.

#### `internal/client/pipeline_test.go`

Интеграционные тесты клиентской control/receive логики на локальных UDP sockets.

- `joinTestPeer` моделирует сервер и умеет принимать request/отправлять response.
- Проверяются пустое имя канала, успешный join, серверный `PacketError`, потерянный ACK с повтором и окончательный timeout.
- Handshake-тесты подтверждают повтор с тем же request ID и финальный timeout.
- Receive-тест доказывает сохранение `SenderID` и `Sequence` из voice packet.
- Отдельно проверяется строгая валидация join response.

### `internal/domain/` — общая модель состояния сервера

#### `internal/domain/channel.go`

Определяет не зависящие от transport и runtime типы для дерева каналов и
будущих snapshots: `ChannelID`, `StateRevision`, `Channel`, `ChannelType`,
`AudioProfile`, `Participant`, `ServerInfo` и client-safe `ServerSnapshot`. Здесь же зафиксированы пределы
имени, темы, описания и глубины дерева, а также единственный текущий профиль
Opus: 48 кГц, mono, frame 20 мс, bitrate 24 кбит/с. `ChannelID == 0` означает,
что участник ещё не присоединился к каналу.

### `internal/voice/` — серверные сессии, control и media routing

#### `internal/voice/session.go`

Модель одной серверной сессии: `ID`, отображаемое `Name`, текущий
`ChannelID`, последний UDP `Addr` и время `LastSeen`. Изменяемые экземпляры
принадлежат `Hub`; наружу выдаются глубокие snapshots.

#### `internal/voice/hub.go`

Потокобезопасный authoritative in-memory реестр сессий и каналов.

- `sync.RWMutex` защищает обе map; session ID генерируются через `crypto/rand`,
  channel ID монотонны и ненулевые в пределах запуска.
- При создании Hub регистрируется постоянный корневой канал `default`.
- `CreateChannel`, `GetChannel` и `ListChannels` управляют
  registry; проверяются metadata, parent, глубина, дубли sibling-имён и
  `MaxUsers`, а список сортируется по `(ParentID, Position, ID)`.
- `StateRevision` меняется только для видимого состояния: lifecycle/rename/move
  участника и создание канала; heartbeat и смена UDP endpoint её не меняют.
- `Inspect` под одним `RLock` возвращает независимый operational snapshot
  server info/revision/channels/sessions для локальной консоли и намеренно
  содержит endpoint и `LastSeen`; `ClientSnapshot` возвращает отдельную
  безопасную копию только с каналами и участниками.
- `CreateSession`, `Add`, `Remove`, `Get` управляют lifecycle.
- `JoinChannel` и routing используют только `ChannelID`; name-based server API
  и name-based wire payload отсутствуют.
- `Rename`, `Touch`, `UpdateAddr` изменяют данные только под write lock.
- `Members`, `SessionsInChannel`, `Recipients`, `RecipientsFor` строят независимые snapshots для чтения/доставки.
- `RecipientsFor` не позволяет маршрутизировать от неизвестной сессии или до join.
- `RemoveInactive` атомарно удаляет просроченные сессии.
- `cloneSession` глубоко копирует и `UDPAddr`, включая IP slice, не позволяя вызывающему создать data race или незаметно изменить внутреннее состояние.

#### `internal/voice/hub_test.go`

Проверяет независимость возвращаемых snapshots, удаление сессий по времени, генерацию ненулевых уникальных ID, повтор генерации при нуле/коллизии, обработку ошибки источника случайности и конкурентную работу join/touch/routing/cleanup. Последний сценарий особенно важен для запуска под race detector.

#### `internal/voice/channel_test.go`

Проверяет стартовый канал, монотонность и порядок `ChannelID`, независимость
domain snapshots, пределы metadata, parent hierarchy, sibling names, циклы,
глубину, `MaxUsers`, семантику `StateRevision`, сортировку участников и
параллельные join/remove/list операции.

#### `internal/voice/cache.go`

Ограниченный потокобезопасный кэш ответов на повторные control-запросы.

- Обычный ключ — `(SessionID, RequestID)`; handshake до появления session использует `(IP:port, RequestID)`.
- TTL по умолчанию 30 секунд, максимум 4096 записей.
- При вставке удаляет expired entries, а на пределе — запись с самым ранним expiry.
- Payload копируется при записи и чтении, исключая внешнюю мутацию.
- `RemoveSession` чистит данные при disconnect/timeout.
- Внутренний конструктор принимает clock, TTL и limit для детерминированных тестов.

#### `internal/voice/cache_test.go`

Проверяет expiry, eviction самой старой записи при capacity limit, очистку по session ID, защитное копирование payload и раздельные handshake entries для разных endpoints с одинаковым request ID.

#### `internal/voice/control.go`

Серверные handlers control packet types.

- `HandleHelloPacket` валидирует request ID и имя до 64 байт, обеспечивает handshake deduplication и создаёт session.
- `HandleHeartbeatPacket` валидирует адрес и обновляет `LastSeen`.
- `HandleDisconnectPacket` удаляет session и её cache entries.
- `HandleJoinChannelPacket` валидирует endpoint и request ID, декодирует
  `ChannelID`, возвращает cached response для duplicate request и кэширует
  ACK/Error. Неизвестный или заполненный канал отклоняется и не создаётся.
- `SendError` и `cacheAndSendHandshakeError` формируют protocol error responses.

#### `internal/voice/snapshot.go`

Обрабатывает metadata/page requests из client-safe копии Hub. Сравнивает
revision, формирует bounded страницы по limit и byte budget, поддерживает
каноническую пустую финальную страницу и использует общий request cache.

#### `internal/voice/delivery.go`

Общая доставка и защита привязки сессии к UDP endpoint.

- `SendToSession`/`SendToSessions` отправляют packet по snapshot-адресам; первая ошибка останавливает массовую отправку.
- `ValidateSessionAddr` сравнивает IP и port фактического отправителя с сохранённой сессией, блокируя простое использование чужого `SessionID` с другого endpoint.
- `UpdateSessionAddr` делегирует смену адреса Hub, но текущий runtime её не вызывает.

Это проверка endpoint binding, а не полноценная аутентификация или криптографическая защита.

#### `internal/voice/media.go`

Серверный media path. `HandleVoicePacket` проверяет endpoint, обновляет активность отправителя и вызывает routing. `FindRecipients` получает участников канала через `Hub.RecipientsFor`, а `RouteVoicePacket` пересылает неизменённый packet всем найденным адресатам.

#### `internal/voice/server.go`

Главный UDP receive loop и dispatcher.

- `HandlePacket` направляет пять допустимых входящих клиентских типов в соответствующие handlers; server-response types от клиента отклоняются.
- `ServeUDP` читает пакеты последовательно и обрабатывает их в той же goroutine.
- Вход, классифицированный как `ErrRejectedDatagram` (malformed, oversized, unknown type и будущие authentication/replay failures), тихо отбрасывается без завершения сервера и без возможности устроить log flood.
- Ошибки конкретного handler также логируются, после чего loop продолжает работу.
- Context cancellation будит заблокированный `ReadFromUDP` через deadline; закрытый socket без отменённого context считается ошибкой.

#### `internal/voice/router_test.go`

Интеграционные тесты серверного dispatcher/routing слоя. Покрывают защиту endpoint binding, запрет voice до join, выбор только получателей того же `ChannelID`, различие sender и recipient/key-owner, deduplication join и hello, ошибки неизвестного/заполненного канала без автоматического создания, остановку `ServeUDP` по context, ошибку закрытого socket и продолжение работы после malformed packet. Вспомогательный `receiveTestPacket` читает реальные ответы через codec-границу.

### `internal/server/` — фоновые процессы серверного приложения

#### `internal/server/config.go`

Граница загрузки стартового состояния. Нейтральный `BootstrapSource` отделяет
bootstrap от формата хранения; `BuiltinBootstrapSource` возвращает имя сервера
и `main`, а `JSONBootstrapSource` строго декодирует файл размером до 64 КиБ. Bootstrap
ограничивает дерево 256 каналами, создаёт parent-first через `Hub.CreateChannel`
и возвращает Hub только после полного успеха. JSON не назначает runtime ID.

#### `internal/server/config_test.go`

Проверяет встроенный `main`, дерево и parent-first ID, пустой список,
malformed/unknown/trailing/oversized JSON, depth/count/metadata/duplicate
ошибки, отмену context, атомарность bootstrap и routing в загруженном канале.

#### `internal/server/console.go`

Локальная read-only консоль с внедряемыми input/output и clock. Поддерживает
`help`, `status`, `channels`, `channel <id|name>`, `users` и
`user <session-id>`. Вывод строится из одного `Hub.Inspect()` snapshot,
детерминированно сортируется, экранирует имена через quoted formatting и не
меняет состояние. Строка команды ограничена 4 КиБ; EOF отключает лишь консоль.

#### `internal/server/console_test.go`

Проверяет точный вывод всех команд, ошибки parser/lookup, неоднозначные имена,
EOF, oversized input, отмену context и конкурентную инспекцию во время
join/remove/heartbeat под race detector.

#### `internal/server/pipeline.go`

Содержит `CleanupLoop` и его operational constants. Раз в `CleanupInterval` удаляет из Hub сессии, неактивные дольше `SessionTimeout`, очищает их request-cache entries, логирует timeout и удаляет остальные expired cache entries. Возвращает `ctx.Err()` при остановке; `cmd/server` нормализует ожидаемый `context.Canceled`.

### `readme/` — рабочие заметки и настройка окружения

#### `readme/info.md`

Черновые инженерные заметки: фрагменты диагностического логирования и схемы handshake, heartbeat, client loops, demultiplexing, join flow и server-side request deduplication. Часть терминологии и схем отражает процесс разработки, поэтому этот файл не следует считать нормативной документацией; актуальные имена и порядок нужно сверять с кодом и `PROJECT_MAP.md`.

#### `readme/sc.ps1`

PowerShell-скрипт, записывающий пользовательские переменные окружения `GOROOT=D:\_go\sdk\go1.27.1` и `GOPROXY=https://proxy.golang.org,direct`. Изменяет постоянное окружение текущего пользователя Windows, не запускается автоматически и не нужен в CI.

### `.idea/` — локальное состояние IntelliJ IDEA/GoLand

Каталог целиком исключён корневым `.gitignore`; файлы машинно-зависимы и не участвуют в Go build.

#### `.idea/go.imports.xml`

Настройка auto-import: исключает устаревший `golang.org/x/net/context`, подталкивая IDE использовать стандартный `context`.

#### `.idea/govots.iml`

Внутреннее описание IDEA-модуля с включённой поддержкой Go module. Дублирует по роли корневой `govots.iml`, но имеет другой IDEA module type/content root.

#### `.idea/misc.xml`

Общая project SDK/output настройка IDEA; сейчас содержит Java SDK `openjdk-26` и каталог `out`. На Go SDK из `go.mod` не влияет.

#### `.idea/modules.xml`

Регистрирует корневой `govots.iml` в IDEA Project Module Manager.

#### `.idea/vcs.xml`

Привязывает проект к Git для IDE-интеграции.

#### `.idea/workspace.xml`

Большой персональный файл состояния IDE: локальный changelist, GOROOT, временные run/test/npm configurations, история commit messages, UI и indexing preferences. Он часто меняется автоматически и не должен использоваться как источник архитектурной истины или попадать в Git.

## 6. Владение состоянием и конкурентность

| Состояние/ресурс | Владелец | Защита/правило |
|---|---|---|
| Серверные sessions/channels | `voice.Hub` | `sync.RWMutex`, наружу только глубокие snapshots |
| Серверные cached responses | `voice.RequestCache` | `sync.Mutex`, payload копируется |
| Клиентский session/channel/pending | `client.State` | `sync.RWMutex` + atomic request counter |
| Один входящий UDP socket клиента | `ReceiveLoop` | после handshake читает только одна goroutine |
| Исходящий microphone codec | `runSession`/`EncodeLoop` | один encoder, один последовательный поток |
| Входящие codec states | `streamDecoders` | отдельный decoder на `SenderID`, доступ из одного loop |
| Jitter state | `streamJitterBuffers` | отдельный buffer на `SenderID`, доступ из одного loop |
| Mixer queues | `pcmMixer` | принадлежат одной `MixLoop` goroutine |
| Audio devices | `runSession` | закрываются один раз; реализации дополнительно идемпотентны |
| Клиентские goroutine | `loopSupervisor` | общая отмена, wait и сбор ошибок |

Сервер сейчас обрабатывает входящие UDP-пакеты последовательно; mutex в `Hub` и `RequestCache` всё равно нужен из-за параллельного `CleanupLoop` и для безопасного дальнейшего распараллеливания.

## 7. Тестовая карта

| Область | Файлы | Что защищается |
|---|---|---|
| Wire protocol | `internal/protocol/proto_test.go` | layout, round trip, type и size limits |
| UDP transport | `internal/transport/udp/*_test.go` | context, recipient ID, socket modes, hard wire limit, ownership и lifecycle |
| Client requests | `internal/client/pipeline_test.go` | handshake/join retry, ACK/error/timeout, receive demux и продолжение после rejected datagram |
| Jitter | `internal/client/jitter_test.go` | reorder, loss, duplicate, old, wraparound, stream isolation |
| Decode | `internal/client/decode_test.go` | decoder-per-sender и cleanup |
| Mixer | `internal/client/mixer_test.go` | суммирование, saturation, очереди sender-ов |
| Server state | `internal/voice/hub_test.go`, `channel_test.go` | snapshots, channel hierarchy/limits/revision, expiry, concurrent access |
| Server bootstrap | `internal/server/config_test.go` | strict JSON, hierarchy/count limits, atomic construction и configured routing |
| Server console | `internal/server/console_test.go` | parser, deterministic inspection, EOF/limits и concurrent mutations |
| Request cache | `internal/voice/cache_test.go` | TTL, capacity, copies, session/endpoint keys |
| Server routing | `internal/voice/router_test.go` | endpoint validation, channel routing, dedup, server lifecycle |
| Client lifecycle | `cmd/client2/supervisor_test.go` | cancellation, shutdown order, отсутствие зависания |

Основные команды проверки:

```bash
go test ./...
go vet ./...
go test -race ./...
```

Полный end-to-end тест с реальными microphone/player устройствами отсутствует: аудиоустройства проверяются при ручном запуске, а автоматические тесты сосредоточены на чистой логике и локальном UDP.

## 8. Быстрая навигация по задачам

| Если нужно изменить… | Начать с | Затем проверить |
|---|---|---|
| Wire format или новый packet type | `internal/protocol/packet.go` | transport, `voice.HandlePacket`, `client.ReceiveLoop`, protocol tests |
| Handshake/retry | `internal/client/request.go` | `internal/voice/control.go`, cache, pipeline/router tests |
| Каналы и выбор получателей | `internal/domain/channel.go`, `internal/voice/hub.go` | control/media handlers и channel/hub/router tests |
| Захват микрофона | `internal/audio/malgo.go` | `client.RecordLoop`, runtime shutdown |
| Opus-параметры | `cmd/client2/runtime.go`, `internal/audio/opus.go` | frame size, payload limit, decoder-per-sender |
| Обработку потерь/порядка | `internal/client/jitter.go` | jitter tests и Opus PLC roadmap |
| Смешивание говорящих | `internal/client/mixer.go` | mixer tests, громкость/clipping policy |
| Воспроизведение | `internal/audio/player.go` | `client.PlaybackLoop`, supervisor shutdown |
| Client lifecycle | `cmd/client2/runtime.go`, `supervisor.go` | supervisor tests и blocking device I/O |
| Server lifecycle | `cmd/server/main.go`, `voice/server.go` | cleanup loop и router lifecycle tests |
| Стартовые каналы/JSON | `internal/server/config.go` | Hub channel validation и config tests |
| Локальный server CLI | `internal/server/console.go` | `Hub.Inspect`, console tests и operational field exposure |
| Session timeout | `internal/server/pipeline.go` | `Hub.RemoveInactive`, cache cleanup |
| CI/toolchain | `.github/workflows/test.yml`, `go.mod` | ALSA dependency и race detector |

## 9. Существенные текущие ограничения

- Нет аутентификации, шифрования, HMAC/AEAD и защиты от replay; проверка `IP:port` — лишь базовая привязка endpoint.
- `SessionID` является криптографически случайным 64-битным идентификатором, но до появления аутентификации и защиты пакетов его всё равно нельзя считать полноценным session token или единственным средством авторизации.
- Клиентский host/port и серверный port пока зашиты в коде; server config
  описывает имя сервера и стартовые каналы.
- Один UDP socket переносит control и media; при нагрузке они конкурируют за одну очередь.
- Сервер выполняет рассылку последовательно и прекращает `SendToSessions` после первой ошибки отправки.
- Jitter buffer фиксированный; нет adaptive jitter, PLC и детальной наружной телеметрии loss/duplicate/drop.
- Mixer использует простое суммирование с saturation; нет master/per-user volume, limiter, mute/deafen, PTT или VAD.
- Reconnect/session recovery реализован, но ещё нет live events и
  периодического revision check; полный список пользователей/каналов
  синхронизируется при подключении, join и по команде `/channels`.
- Нет persistent storage: все sessions/channels/cache существуют только в памяти процесса.
- Серверная консоль локальная и read-only; удалённого RCON пока нет.
- CLI-команда `/join` может выполняться параллельно с shutdown; полноценного GUI пока нет.

## 10. Минимальный запуск

```bash
# Терминал 1
go run ./cmd/server

# Терминал 2
go run ./cmd/client2 -name alice -channel main

# Терминал 3
go run ./cmd/client2 -name bob -channel main
```

Во время работы клиента доступны:

```text
/join main
/quit
```
