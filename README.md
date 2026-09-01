# Go Voice MVP

Рабочий TeamSpeak-like networking MVP без внешних Go-зависимостей.

## Уже реализовано

- control plane: TCP + JSON Lines (`:8080`);
- media plane: UDP (`:9987`);
- создание случайной session id;
- join в channel;
- список участников канала;
- UDP HELLO, связывающий session id с наблюдаемым `IP:port`;
- forwarding voice-пакетов только другим участникам того же канала;
- sequence number;
- ограничение media payload до 1200 bytes;
- server treats media payload as opaque bytes — готово под Opus.

## Запуск

```bash
go run ./cmd/server
```

В двух других терминалах:

```bash
go run ./cmd/client -name alice -channel general
go run ./cmd/client -name bob   -channel general
```

У Alice:

```text
/say hello
/burst 20
```

Bob получит пакеты через UDP. Если Bob подключится с `-channel gaming`, он их не получит.

## Протокол control plane

Клиент -> сервер:

```json
{"type":"hello","name":"alice"}
{"type":"join","channel":"general"}
{"type":"members"}
```

Сервер -> клиент:

```json
{"type":"welcome","name":"alice","session_id":123}
{"type":"joined","channel":"general","members":["alice","bob"]}
```

## UDP packet

```text
magic       uint32   4 bytes
version     uint8    1 byte
packet_type uint8    1 byte
session_id  uint64   8 bytes
sequence    uint32   4 bytes
payload     []byte
```

`packet_type=1` — HELLO. `packet_type=2` — VOICE.

## Где вставится настоящий Opus

Сервер менять практически не надо:

```text
microphone -> PCM -> Opus encode -> VoicePacket.Payload
                                      |
                                      v
                                 UDP server
                                      |
                                      v
                                other clients
                                      |
                                      v
                          jitter buffer -> Opus decode -> speaker
```

Текущие `/say` и `/burst` просто подставляют тестовые bytes вместо Opus frame.

## Следующий разумный шаг

1. HMAC/AEAD-аутентификация UDP пакетов.
2. Ping/keepalive и session timeout.
3. Настоящий Opus client adapter.
4. Jitter buffer + packet-loss accounting.
5. mute/deafen и speaking events.
6. После этого — WebSocket/QUIC при необходимости.
