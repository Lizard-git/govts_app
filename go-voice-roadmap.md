# Дорожная карта Go Voice

## Назначение

Здесь находится укрупнённое направление продукта. Фактическое состояние
описано в [`README.md`](README.md), текущий порядок — в
[`readme_docs/development-plan.md`](readme_docs/development-plan.md), а
непринятые идеи — в [`readme_docs/backlog.md`](readme_docs/backlog.md).

## Продуктовая цель

Govts развивается как самостоятельное desktop-приложение для голосового
общения: иерархические каналы, качественный многопользовательский звук,
восстановление соединения и понятные локальные настройки.

## Реализованная основа

- bounded UDP protocol с retry и deduplication control requests;
- sessions, heartbeat, timeout, disconnect и reconnect;
- иерархические каналы, revisioned snapshot и live events;
- отдельные decoder/jitter buffer на говорящего, mixer и speaking state;
- Opus, RNNoise и независимый WebRTC VAD/voice gate;
- mute, deafen и горячий выбор input/output devices;
- общий `clientapp`, CLI adapter и Wails/React desktop-клиент;
- UI каналов и настроек, журнал событий и persistence локальных настроек.

История основы находится в `readme_docs/patch-1.md` — `patch-7.md`.

## Ближайшие этапы

```text
приёмка и стабилизация первого desktop UI
    ↓
persistence серверной конфигурации и каналов
    ↓
create/edit/reorder/delete channel contracts и local admin UI
    ↓
volume controls, per-user volume и Push-to-Talk
    ↓
диагностика сети и профили качества
    ↓
adaptive jitter, Opus PLC/FEC и адаптация bitrate
```

Каждый этап начинается с отдельного patch-плана и backend-контракта. UI не
опережает доступную серверную или audio-семантику.

## Условие внешнего использования

Перед недоверенной сетью необходим отдельный security-этап: threat model,
authentication, server identity, authorization, защищённый handshake и
datagrams, replay protection, rate limiting и protocol versioning. Простой
`SessionID` не является подтверждением личности.

## Поздние направления

- installer, signing, updater и release matrix;
- роли, permissions и moderation;
- channel chat и история сообщений;
- avatars и доставка assets;
- screen sharing и отдельный media pipeline;
- альтернативный transport после измерений текущего UDP path.

Условия их активации поддерживаются в
[`readme_docs/backlog.md`](readme_docs/backlog.md).
