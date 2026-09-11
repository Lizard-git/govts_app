# Go Voice — продуктовая перспектива

Этот документ хранит широкую картину развития и часть исторических этапов.
Чекбоксы ниже могут отставать от фактической реализации и не задают текущую
очерёдность. Актуальный исполняемый план находится в
[`readme_docs/development-plan.md`](readme_docs/development-plan.md), отложенные
идеи — в [`readme_docs/backlog.md`](readme_docs/backlog.md).

## Цель проекта

Построить полноценное приложение голосовой связи:

-   клиент-серверная архитектура;
-   голосовые каналы;
-   несколько одновременно говорящих пользователей;
-   устойчивость к потерям и перестановке UDP-пакетов;
-   синхронизация состояния пользователей и каналов;
-   reconnect;
-   управление микрофоном и звуком;
-   аутентификация и шифрование;
-   GUI;
-   production hardening.

------------------------------------------------------------------------

## Текущее состояние

Уже реализовано:

-   [x] UDP transport
-   [x] Opus encode/decode
-   [x] захват микрофона
-   [x] воспроизведение звука
-   [x] передача голоса между клиентами
-   [x] серверная маршрутизация voice packets
-   [x] sessions
-   [x] channels
-   [x] handshake
-   [x] heartbeat
-   [x] session timeout
-   [x] graceful disconnect
-   [x] runtime `/join`
-   [x] разделение voice/control packets
-   [x] client-side `State`
-   [x] `RequestID`
-   [x] pending control requests
-   [x] `DoRequest`
-   [x] `PacketError`
-   [x] ACK/Error для control requests

------------------------------------------------------------------------

# Этап 1. Reliable Control Protocol

**Статус: завершён**

Цель: сделать control-команды над UDP устойчивыми к потере пакетов и не
допускать повторного выполнения одной команды на сервере.

## Задачи

-   [x] retry внутри `DoRequest`
-   [x] использовать один `RequestID` для всех retry одного запроса
-   [x] добавить server-side `RequestCache`
-   [x] ключ кеша: `(SessionID, RequestID)`
-   [x] server-side deduplication
-   [x] кешировать успешные ACK
-   [x] кешировать `PacketError`
-   [x] TTL для записей request cache
-   [x] периодическая очистка request cache
-   [x] тест duplicate request
-   [x] тест потерянного ACK
-   [x] тест retry
-   [x] тест окончательного timeout
-   [x] тест позднего ACK после timeout

## Требуемая семантика

``` text
Client                         Server

RequestID=42 ────────────────► execute request
                               create response
                               cache response
              ◄────────────── ACK #42
                    X
                 lost

timeout

RequestID=42 ────────────────► cache hit
                               DO NOT execute again
              ◄────────────── cached ACK #42
```

`RequestID` создаётся один раз на логическую операцию:

``` text
attempt 1 → RequestID=42
attempt 2 → RequestID=42
attempt 3 → RequestID=42
```

`Sequence` для этого не используется. Он остаётся идентификатором
порядка voice packets.

## Результат этапа

Control request выполняется сервером не более одного раза в пределах
жизни записи deduplication cache, а потерянный response может быть
отправлен повторно.

------------------------------------------------------------------------

# Этап 2. Модель состояния сервера и клиента

Цель: сервер становится authoritative source состояния приложения.

## Control-команды

Планируем добавить:

-   [ ] `ListChannels`
-   [ ] `ListUsers`
-   [ ] `CreateChannel`
-   [ ] расширить `JoinChannel`
-   [ ] при необходимости `LeaveChannel`

Позже:

-   [ ] rename user
-   [ ] move user
-   [ ] delete channel
-   [ ] kick user
-   [ ] permissions

## Server events

Нужно разделить request/response и события.

### Request/response

``` text
Client                         Server

ListUsers #17 ───────────────►
              ◄────────────── ListUsersResponse #17
```

У response тот же `RequestID`.

### Event

``` text
Client                         Server

              ◄────────────── UserJoined
              ◄────────────── UserLeft
              ◄────────────── UserMoved
              ◄────────────── ChannelCreated
              ◄────────────── ChannelRemoved
```

Event не является ответом на запрос, поэтому ему не нужен `RequestID`.

## Client State

Постепенно состояние клиента должно хранить:

``` text
Session
Current user
Current channel
Channels
Users
Connection state
Pending requests
```

## Синхронизация

После подключения:

``` text
Handshake
   ↓
initial state sync
   ↓
ListChannels
ListUsers
   ↓
State initialized
   ↓
дальше State обновляется server events
```

## Результат этапа

Клиент всегда знает:

-   какие каналы существуют;
-   какие пользователи подключены;
-   где находится каждый пользователь;
-   в каком канале находится текущий клиент.

------------------------------------------------------------------------

# Этап 3. Нормальная модель входящего Voice Packet

Сейчас нельзя терять сетевые метаданные после `ReceiveLoop`.

В voice pipeline должны сохраняться:

``` text
SessionID
Sequence
Payload
```

Добавить отдельную модель, например:

``` go
type IncomingVoiceFrame struct {
    SenderID uint64
    Sequence uint32
    Data     []byte
}
```

Pipeline:

``` text
UDP
 ↓
VoicePacket
 ↓
ReceiveLoop
 ↓
IncomingVoiceFrame
 ↓
voice processing
```

## Задачи

-   [ ] перестать превращать входящий packet сразу в обезличенный
    `audio.Frame`
-   [ ] сохранить `SessionID`
-   [ ] сохранить `Sequence`
-   [ ] отделить network voice frame от PCM/audio frame
-   [ ] подготовить dispatcher по `SenderID`

## Результат этапа

Клиент понимает:

-   кто отправил voice frame;
-   какой у него sequence number;
-   к какому voice stream относится пакет.

------------------------------------------------------------------------

# Этап 4. Multi-speaker Voice Architecture

Цель: корректно поддерживать несколько одновременно говорящих
пользователей.

Нельзя использовать один Opus decoder для всех remote speakers.

Архитектура:

``` text
                    ┌─ Speaker A stream
                    │      ↓
                    │   decoder A
                    │
UDP → dispatcher ───┼─ Speaker B stream
                    │      ↓
                    │   decoder B
                    │
                    └─ Speaker C stream
                           ↓
                        decoder C
```

Для каждого remote speaker понадобится собственное состояние:

``` go
type RemoteStream struct {
    SessionID uint64
    Decoder   audio.Decoder

    // позже:
    // jitter buffer
    // sequence state
    // volume
}
```

## Задачи

-   [ ] `RemoteStream`
-   [ ] registry активных streams
-   [ ] создание stream при первом voice packet
-   [ ] отдельный decoder на пользователя
-   [ ] удаление stream при disconnect/user-left
-   [ ] безопасная concurrent работа registry

## Результат этапа

Несколько пользователей могут одновременно передавать Opus frames без
смешивания состояния decoder'ов.

------------------------------------------------------------------------

# Этап 5. Sequence Tracking и Jitter Buffer

UDP не гарантирует:

-   доставку;
-   порядок;
-   отсутствие дублей.

Например сервер может получить:

``` text
100
101
103
102
105
```

Pipeline должен стать:

``` text
UDP
 ↓
speaker dispatcher
 ↓
sequence tracking
 ↓
jitter buffer
 ↓
Opus decoder
 ↓
PCM
```

## Задачи

-   [ ] tracking ожидаемого `Sequence`
-   [ ] duplicate detection
-   [ ] reorder packets
-   [ ] late packet drop
-   [ ] packet loss detection
-   [ ] jitter buffer
-   [ ] configurable playback delay
-   [ ] обработка пропущенных frames
-   [ ] Opus PLC, если decoder/API позволяет
-   [ ] статистика packet loss
-   [ ] статистика jitter

## Результат этапа

Голос остаётся пригодным для разговора при реальной нестабильной сети.

------------------------------------------------------------------------

# Этап 6. Audio Mixer

После декодирования нескольких streams получаем несколько PCM-потоков.

Нужно:

``` text
Alice PCM ──┐
Bob PCM   ──┼──► Mixer ──► clipping protection ──► Player
Carol PCM ──┘
```

## Задачи

-   [ ] синхронизация PCM frames
-   [ ] mixer
-   [ ] clipping protection
-   [ ] общий output volume
-   [ ] per-user volume
-   [ ] mute remote user
-   [ ] удаление inactive streams

## Результат этапа

Полноценный групповой voice channel.

------------------------------------------------------------------------

# Этап 7. Voice Controls

После стабильного audio pipeline добавляем пользовательские функции.

## Задачи

-   [ ] microphone mute
-   [ ] deafen
-   [ ] push-to-talk
-   [ ] input gain
-   [ ] output volume
-   [ ] per-user volume
-   [ ] mute конкретного пользователя
-   [ ] speaking indicator
-   [ ] выбор input device
-   [ ] выбор output device

## VAD

Позже добавить Voice Activity Detection:

``` text
Microphone
   ↓
VAD
   ├─ silence → не отправлять
   └─ speech  → Opus → UDP
```

Это позволит:

-   уменьшить bandwidth;
-   показывать speaking indicator;
-   не отправлять постоянный фоновый шум.

------------------------------------------------------------------------

# Этап 8. Reconnect и Session Recovery

Кратковременный сетевой обрыв не должен означать полный выход
пользователя.

Желаемая state machine:

``` text
Disconnected
     ↓
Connecting
     ↓
Connected
     ↓
network failure
     ↓
Reconnecting
     ↓
session resume
     ↓
state synchronization
     ↓
Connected
```

## Задачи

-   [ ] connection state
-   [ ] обнаружение потери сервера
-   [ ] reconnect loop
-   [ ] exponential backoff
-   [ ] session/resume token
-   [ ] восстановление identity
-   [ ] восстановление channel
-   [ ] повторная синхронизация server state
-   [ ] очистка старых pending requests
-   [ ] корректное восстановление voice streams

## Результат этапа

Краткий обрыв Wi-Fi/интернета не требует ручного переподключения
пользователя.

------------------------------------------------------------------------

# Этап 9. Security

До использования через публичный интернет необходимо решить
безопасность.

## Задачи

-   [ ] authentication
-   [ ] session credentials
-   [ ] authorization
-   [ ] permissions
-   [ ] защита от session spoofing
-   [ ] защита control protocol
-   [ ] encryption
-   [ ] replay protection
-   [ ] rate limiting
-   [ ] validation всех входящих packets

Нельзя считать простой `SessionID` доказательством личности клиента.

------------------------------------------------------------------------

# Этап 10. Разделение Control Plane и Voice Plane

После реализации и изучения reliability поверх UDP можно решить, какой
transport нужен окончательной архитектуре.

Логическая модель:

``` text
                ┌─ Control plane
Client ─────────┤
                └─ Voice plane
```

Control требует:

-   надёжности;
-   порядка;
-   authentication;
-   encryption.

Voice требует:

-   низкой latency;
-   допуска потерь;
-   datagram semantics.

На этом этапе рассмотреть:

-   TCP/TLS + UDP;
-   QUIC;
-   QUIC streams + datagrams;
-   другую подходящую транспортную схему.

Решение принимается после измерений и тестов, а не заранее.

------------------------------------------------------------------------

# Этап 11. GUI

GUI имеет смысл строить поверх уже нормальной модели клиента.

UI не должен напрямую работать с:

-   UDP;
-   Opus;
-   `RequestID`;
-   jitter buffer;
-   transport internals.

Backend клиента должен предоставлять UI состояние уровня:

``` text
Connection
CurrentUser
CurrentChannel
Channels
Users
Speaking
Muted
Deafened
Volume
Ping
PacketLoss
```

Пример:

``` text
┌──────────────────────────────────────────┐
│ Voice                                    │
├──────────────┬───────────────────────────┤
│ Channels     │ General                   │
│              │                           │
│ General      │ ● Alice                   │
│ Gaming       │ ○ Bob                     │
│ Music        │ ● Carol                   │
│              │                           │
├──────────────┴───────────────────────────┤
│ Mute       Deafen              Settings  │
└──────────────────────────────────────────┘
```

## Задачи

-   [ ] выбор GUI framework
-   [ ] connection screen
-   [ ] channel list
-   [ ] user list
-   [ ] join channel
-   [ ] speaking indicator
-   [ ] mute/deafen
-   [ ] volume controls
-   [ ] settings
-   [ ] audio device selection
-   [ ] connection/reconnect status

------------------------------------------------------------------------

# Этап 12. Production Hardening

## Protocol

-   [ ] packet size limits
-   [ ] malformed packet protection
-   [ ] protocol version
-   [ ] fuzz tests decoder'а
-   [ ] unknown packet handling
-   [ ] backwards compatibility strategy

## Concurrency

-   [ ] `go test -race`
-   [ ] audit goroutine lifetime
-   [ ] audit channel ownership
-   [ ] audit mutex usage
-   [ ] shutdown tests
-   [ ] leak detection

## Network testing

Проверить работу при:

``` text
packet loss:
0%
1%
5%
10%

jitter:
20 ms
50 ms
100 ms

дополнительно:
packet reorder
duplicates
bursty loss
disconnect
reconnect
server restart
```

## Performance

-   [ ] CPU profiling
-   [ ] memory profiling
-   [ ] allocations в audio hot path
-   [ ] network bandwidth
-   [ ] latency measurements
-   [ ] mixer performance
-   [ ] нагрузочные тесты сервера

## Application

-   [ ] structured logging
-   [ ] metrics
-   [ ] configuration
-   [ ] Windows support
-   [ ] Linux support
-   [ ] macOS support
-   [ ] packaging
-   [ ] versioning
-   [ ] update strategy

------------------------------------------------------------------------

# Общая дорожная карта

``` text
[✓] UDP transport
[✓] Opus
[✓] microphone
[✓] playback
[✓] voice routing
[✓] sessions
[✓] channels
[✓] handshake
[✓] heartbeat / timeout
[✓] graceful disconnect
[✓] runtime channel switching
[✓] control packet routing
[✓] RequestID
[✓] pending requests
[✓] DoRequest
[✓] PacketError

[✓] Reliable Control Protocol
     ├─ retry
     ├─ deduplication
     ├─ RequestCache
     └─ TTL

[ ] State Synchronization
     ├─ ListChannels
     ├─ ListUsers
     └─ server events

[ ] Voice Packet Model
     ├─ SenderID
     └─ Sequence

[ ] Multi-speaker Audio
     ├─ RemoteStream
     └─ decoder per speaker

[ ] Network Audio Reliability
     ├─ sequence tracking
     ├─ jitter buffer
     └─ packet loss / PLC

[ ] Audio Mixer
     ├─ mixing
     └─ per-user volume

[ ] Voice Controls
     ├─ mute
     ├─ deafen
     ├─ PTT
     └─ VAD

[ ] Reconnect / Session Recovery

[ ] Authentication / Security

[ ] Final Transport Architecture

[ ] GUI

[ ] Production Hardening
```

------------------------------------------------------------------------

# Ближайшие спринты

## Sprint 1 --- Control Reliability

-   [x] retry в `DoRequest`
-   [x] один `RequestID` на retry
-   [x] `RequestCache`
-   [x] deduplication
-   [x] cache ACK/Error
-   [x] TTL
-   [x] cleanup
-   [x] network-loss tests

## Sprint 2 --- Server State

-   [ ] `ListChannels`
-   [ ] `ListUsers`
-   [ ] users/channels в client `State`
-   [ ] initial state synchronization
-   [ ] `UserJoined`
-   [ ] `UserLeft`
-   [ ] `UserMoved`
-   [ ] channel events

## Sprint 3 --- Multi-speaker Voice

-   [ ] сохранить `SenderID`
-   [ ] сохранить `Sequence`
-   [ ] `RemoteStream`
-   [ ] decoder per speaker
-   [ ] stream lifecycle
-   [ ] несколько одновременных speakers

## Sprint 4 --- Real Network Audio

-   [ ] sequence tracking
-   [ ] jitter buffer
-   [ ] reorder
-   [ ] duplicate handling
-   [ ] loss handling
-   [ ] PLC
-   [ ] network simulation tests

## Sprint 5 --- Mixer и Voice Controls

-   [ ] mixer
-   [ ] clipping protection
-   [ ] volume
-   [ ] per-user volume
-   [ ] mute
-   [ ] deafen
-   [ ] PTT
-   [ ] speaking state
-   [ ] VAD

------------------------------------------------------------------------

## Текущая точка

Sprint 1 --- Control Reliability завершён.

После обязательных ограничений wire protocol и надёжного handshake переходим к
пользовательской функциональности:

**State Synchronization → Multi-speaker Voice → Jitter Buffer → Mixer.**
