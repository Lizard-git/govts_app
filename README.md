# Govts

Govts — настольный клиент голосовой связи и сервер на Go. Проект поддерживает
каналы, обработку звука и демонстрацию экрана. Go-модуль — `uniclog.io/govts`.

Подробная структура кода описана в [`PROJECT_MAP.md`](PROJECT_MAP.md). Рабочие заметки в
`readme_docs/` хранятся локально и не входят в Git-репозиторий.

## Текущая архитектура

Клиент и сервер используют UDP transport (по умолчанию порт `9000`) для
control-пакетов и Opus voice-пакетов. Демонстрация экрана использует встроенный
в тот же сервер HTTPS signaling и WebRTC/DTLS-SRTP с Pion SFU.

```text
microphone
    -> PCM frames
    -> RNNoise filter (optional)
    -> WebRTC VAD + voice gate (optional)
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

### Реализовано

- UDP handshake `Hello` / `HelloAck`;
- server-side sessions с криптографически случайным `SessionID`;
- самостоятельная доменная модель каналов со стабильным в пределах запуска
  `ChannelID`, иерархией, метаданными и фиксированным Opus-профилем;
- потокобезопасный server-side registry каналов, участников и
  `StateRevision`;
- встроенный канал `default` без конфигурационного файла и строгая загрузка
  полного стартового дерева каналов из bounded JSON-конфигурации;
- локальная read-only консоль сервера для просмотра status, каналов и
  подключённых пользователей;
- подтверждаемый heartbeat каждые 5 секунд с отдельным ACK deadline 3 секунды;
- автоматический reconnect с задержками `1s → 2s → 4s → 4s...`, новой
  сессией и восстановлением канала по пути имён;
- удаление session после 30 секунд неактивности;
- подключение и переключение канала через `JoinChannel`;
- разделение входящих voice и control packets;
- Opus encode/decode;
- захват микрофона через malgo;
- воспроизведение через malgo с переключением устройства вывода;
- `RequestID`, ожидание ответа и повтор control-запроса;
- server-side deduplication через `RequestCache`;
- явный disconnect клиента;
- единая codec-граница для всех логических пакетов с явным направлением,
  endpoint и отдельным контекстом получателя (`KeyOwnerID`);
- разные transport-типы для connected client socket и unconnected server
  socket;
- безопасный drop повреждённых/rejected датаграмм без остановки receive loop;
- paged `ServerSnapshot` и атомарное клиентское состояние;
- live-события подключения, перехода и отключения участников; recovery через
  snapshot и metadata revision check каждые 5 секунд;
- mute/deafen и индикаторы речи в настольном клиенте;
- независимые RNNoise-шумоподавление и WebRTC VAD/voice gate с режимами
  `level`, `vad`, `hybrid`, pre-roll 60 мс и hangover 300 мс;
- `ClientViewState`, глубокие копии и bounded-подписка на изменения UI;
- Wails 3 desktop-клиент с React/TypeScript UI каналов, журналом событий,
  настройками аудио и сохранением локальных параметров;
- громкость и отключение звука отдельных участников, темы оформления и
  окно статистики соединения;
- несколько одновременных демонстраций экрана в канале с добровольной
  подпиской на выбранного автора, VP8 и статичными карточками без превью;
- автоматически создаваемая TLS identity media-сервера и подтверждение её
  SHA-256 отпечатка клиентом при первом подключении (TOFU).

### Известные ограничения

- jitter buffer использует фиксированное окно; ещё нет Opus PLC и адаптивной
  задержки;
- handshake deduplication требует стабильного `IP:port` на время retry;
- учётных записей и аутентификации пользователей пока нет;
- media signaling и WebRTC зашифрованы, но voice/control UDP остаются
  незашифрованными; TURN/TCP fallback и передача системного звука не реализованы;
- нет удалённого RCON, сетевого API создания каналов и persistence;
- live-события доставляются best effort; потеря последнего события обнаруживается
  периодической проверкой revision. Консольная история также best effort.
- VAD распознаёт любую речь, а не владельца микрофона; `hybrid` отсекает фоновый
  разговор только тогда, когда он тише настроенного порога.

## Структура проекта

```text
cmd/server/              запуск UDP-сервера
cmd/desktop/             Wails desktop entrypoint и React frontend
cmd/versionbump/          обновление версий сборок
internal/audio/          устройства, PCM и Opus
internal/appversion/     разбор и сравнение версий
internal/client/         состояние и goroutine клиента
internal/clientapp/      общий lifecycle, reconnect и session orchestration
internal/clientsettings/ локальные настройки desktop-клиента
internal/domain/         общие модели каналов, участников и ревизии
internal/media/          HTTPS signaling и WebRTC media server
internal/protocol/       бинарный формат пакета
internal/server/         bootstrap, локальная консоль и фоновые процессы
internal/transport/udp/  чтение и запись UDP
internal/ui/wails/       bindings, безопасные DTO и Wails event bridge
internal/voice/          sessions, channels, routing и request cache
```

Пакеты внутри `internal` доступны только коду этого Go-модуля. Это позволяет
менять внутреннюю архитектуру, не создавая преждевременный публичный API.

## Запуск

Для сборки требуется Go `1.27.1`. Настольному клиенту также нужны Node.js/npm,
доступные аудиоустройства и WebView2 на Windows. Сервер можно запускать без
аудиоустройств.

Запустить сервер:

```bash
go run ./cmd/server
```

По умолчанию голос и управление используют UDP `9000`, HTTPS media signaling —
TCP `9002`, а WebRTC — UDP `20000–20100`. Для публичного сервера укажите его
достижимый IP и откройте соответствующие порты:

```bash
go run ./cmd/server -media-advertised-ip 203.0.113.10
```

Файлы `govts-media.crt` и `govts-media.key` создаются автоматически. При первом
просмотре или запуске демонстрации клиент показывает отпечаток; его следует
сверить с `fingerprint` в консоли сервера. Публичный CA и домен не требуются.
Screen sharing можно отключить флагом `-media-port 0`; диапазон меняется через
`-media-min-port` и `-media-max-port`.

Другой голосовой порт задаётся через `-port` (1–65535); если `-media-port`
не указан, HTTPS signaling слушает на два порта выше:

```bash
go run ./cmd/server -port 9100
```

В настольном клиенте укажите тот же адрес и порт, например
`192.168.1.50:9100`.

Без конфигурационного файла доступен единственный канал `default`. При запуске
с JSON поле `channels` является полным деревом, должно содержать хотя бы один
канал и не дополняется встроенным `default`:

```bash
go run ./cmd/server -config configs/server.example.json
```

Локальные команды сервера:

```text
help
status
channels
channel <id|name>
users
user <session-id>
```

Настольный клиент использует Wails `v3.0.0-beta.20`, React и TypeScript. Команды
ниже запускают закреплённую версию Wails CLI через Go; отдельно устанавливать
CLI не нужно.

Запустить desktop UI с hot reload:

```powershell
npm run dev:desktop
```

Собрать standalone desktop-клиент:

```powershell
npm run build:desktop
```

Результат на Windows — `bin/Govts.exe`. В интерфейсе доступны подключение,
каналы, статистика соединения, настройки микрофона и воспроизведения, громкость
участников, темы оформления и демонстрация экрана.

Команды сборки через Wails автоматически увеличивают patch-версию в
`cmd/desktop/version.txt` и синхронизируют её с `build/config.yml`. Версия
отображается в заголовке окна. Отдельный релизный workflow собирает код без
повышения версии.

Desktop-клиент сохраняет display name и аудионастройки в
`%APPDATA%\Govts\settings.json`. Сохраняются выбранные устройства ввода и
вывода, deafen, RNNoise, VAD, тему оформления и доверенные отпечатки
media-серверов. Адрес сервера хранится отдельно во frontend `localStorage`.
При первом запуске в поле подключения указан `127.0.0.1:9000`; для другого
сервера введите его IP или адрес с портом, например `192.168.1.50:9000`.

Linux-сервер `amd64` можно собрать на Windows командой
`.\scripts\build-server.ps1`: она повышает `cmd/server/version.txt` и создаёт
`bin/govts-server`. Версию сервера можно проверить флагом `-version`.

## Релизы GitHub

Workflow [`.github/workflows/release.yml`](.github/workflows/release.yml)
запускается при отправке тега `vX.Y.Z`. Он проверяет, что версия тега совпадает
с закоммиченным `cmd/desktop/version.txt`, собирает Windows-клиент и Linux-сервер
и публикует оба файла в GitHub Releases. Перед созданием тега закоммитьте и
отправьте код вместе с нужной версией. Пример для PowerShell:

```powershell
$releaseVersion = (Get-Content cmd/desktop/version.txt -Raw).Trim()
git tag -a "v$releaseVersion" -m "Release v$releaseVersion"
git push origin "v$releaseVersion"
```

## Проверки проекта

```bash
go test ./...
go vet ./...
npm test --prefix cmd/desktop/frontend
npm run build --prefix cmd/desktop/frontend
```

CI также запускает `go test -race ./...` на Linux; для него требуются системные
audio development packages, устанавливаемые workflow.

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
9  StateSnapshotRequest
10 StateSnapshotAck
11 HeartbeatAck
12 SessionInvalid
13 StateEvent
14 MediaCredentialRequest
15 MediaCredentialAck
16 ServerVersionTooOld
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

| Параметр | Значение |
|---|---:|
| UDP read buffer | 1218 bytes |
| Максимальный datagram | 1217 bytes |
| Максимальный payload | 1200 bytes |
| Handshake timeout | 3 секунды на попытку |
| Control request timeout | 3 секунды на попытку |
| Control request attempts | 3 |
| Heartbeat interval | 5 секунд |
| Session timeout | 30 секунд |
| Session cleanup interval | 5 секунд |
